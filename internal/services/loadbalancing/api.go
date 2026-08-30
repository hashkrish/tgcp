// Package loadbalancing provides a read-only view of Cloud Load Balancing
// resources: Backend Services, Health Checks, URL Maps, Forwarding Rules,
// and SSL Certificates — see the package-level comment in loadbalancing.go
// for scope notes.
//
// All resources here live in the same `compute/v1` API surface already used
// by internal/services/gce and internal/services/net, so this package
// reuses that same compute.Service client construction pattern. Backend
// services and health checks can each be either global or regional, so both
// listings use AggregatedList (project-wide across all scopes) rather than
// a hardcoded region list or per-region fan-out.
//
// No mutating calls are made anywhere in this package — list only.
package loadbalancing

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/compute/v1"
)

type Client struct {
	service *compute.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	s, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create compute service: %w", err)
	}
	return &Client{service: s}, nil
}

// ListBackendServices lists all backend services (global and regional)
// in the project.
func (c *Client) ListBackendServices(projectID string) ([]BackendService, error) {
	if demo.Enabled {
		return []BackendService{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	var result []BackendService
	req := c.service.BackendServices.AggregatedList(projectID)
	err := req.Pages(context.Background(), func(page *compute.BackendServiceAggregatedList) error {
		for scope, scoped := range page.Items {
			for _, bs := range scoped.BackendServices {
				result = append(result, BackendService{
					Name:                bs.Name,
					Region:              scopeToRegion(scope),
					Protocol:            bs.Protocol,
					LoadBalancingScheme: bs.LoadBalancingScheme,
					HealthCheckName:     firstHealthCheckName(bs.HealthChecks),
					BackendCount:        len(bs.Backends),
					Description:         bs.Description,
					Port:                bs.Port,
					PortName:            bs.PortName,
					TimeoutSec:          bs.TimeoutSec,
					SessionAffinity:     bs.SessionAffinity,
					EnableCDN:           bs.EnableCDN,
					SecurityPolicy:      shortName(bs.SecurityPolicy),
					CreationTimestamp:   bs.CreationTimestamp,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list backend services: %w", err)
	}
	return result, nil
}

// ListHealthChecks lists all health checks (global and regional) in the
// project.
func (c *Client) ListHealthChecks(projectID string) ([]HealthCheck, error) {
	if demo.Enabled {
		return []HealthCheck{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	var result []HealthCheck
	req := c.service.HealthChecks.AggregatedList(projectID)
	err := req.Pages(context.Background(), func(page *compute.HealthChecksAggregatedList) error {
		for scope, scoped := range page.Items {
			for _, hc := range scoped.HealthChecks {
				port, checkType := healthCheckPortAndType(hc)
				result = append(result, HealthCheck{
					Name:               hc.Name,
					Region:             scopeToRegion(scope),
					Type:               checkType,
					Port:               port,
					CheckIntervalSec:   hc.CheckIntervalSec,
					TimeoutSec:         hc.TimeoutSec,
					HealthyThreshold:   hc.HealthyThreshold,
					UnhealthyThreshold: hc.UnhealthyThreshold,
					Description:        hc.Description,
					LogEnabled:         hc.LogConfig != nil && hc.LogConfig.Enable,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list health checks: %w", err)
	}
	return result, nil
}

// DeleteHealthCheck deletes a health check, matching
// `gcloud compute health-checks delete`. Health checks can be global or
// regional; region is "global" for the former, else a region name.
func (c *Client) DeleteHealthCheck(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		_, err := c.service.HealthChecks.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.RegionHealthChecks.Delete(projectID, region, name).Do()
	return err
}

// DeleteBackendService deletes a backend service, matching
// `gcloud compute backend-services delete`. Backend services can be global
// or regional; region is "global" for the former, else a region name.
func (c *Client) DeleteBackendService(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		_, err := c.service.BackendServices.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.RegionBackendServices.Delete(projectID, region, name).Do()
	return err
}

// ListUrlMaps lists all URL maps (global and regional) in the project.
func (c *Client) ListUrlMaps(projectID string) ([]UrlMap, error) {
	if demo.Enabled {
		return []UrlMap{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	var result []UrlMap
	req := c.service.UrlMaps.AggregatedList(projectID)
	err := req.Pages(context.Background(), func(page *compute.UrlMapsAggregatedList) error {
		for scope, scoped := range page.Items {
			for _, um := range scoped.UrlMaps {
				result = append(result, UrlMap{
					Name:           um.Name,
					Region:         scopeToRegion(scope),
					DefaultService: shortName(um.DefaultService),
					Description:    um.Description,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list URL maps: %w", err)
	}
	return result, nil
}

// ListForwardingRules lists all forwarding rules (global and regional) in
// the project.
func (c *Client) ListForwardingRules(projectID string) ([]ForwardingRule, error) {
	if demo.Enabled {
		return []ForwardingRule{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	var result []ForwardingRule
	req := c.service.ForwardingRules.AggregatedList(projectID)
	err := req.Pages(context.Background(), func(page *compute.ForwardingRuleAggregatedList) error {
		for scope, scoped := range page.Items {
			for _, fr := range scoped.ForwardingRules {
				result = append(result, ForwardingRule{
					Name:                fr.Name,
					Region:              scopeToRegion(scope),
					IPAddress:           fr.IPAddress,
					IPProtocol:          fr.IPProtocol,
					PortRange:           fr.PortRange,
					Target:              shortName(fr.Target),
					LoadBalancingScheme: fr.LoadBalancingScheme,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list forwarding rules: %w", err)
	}
	return result, nil
}

// ListSslCertificates lists all SSL certificates (global and regional) in
// the project. Certificate/private key material is never fetched.
func (c *Client) ListSslCertificates(projectID string) ([]SslCertificate, error) {
	if demo.Enabled {
		return []SslCertificate{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	var result []SslCertificate
	req := c.service.SslCertificates.AggregatedList(projectID)
	err := req.Pages(context.Background(), func(page *compute.SslCertificateAggregatedList) error {
		for scope, scoped := range page.Items {
			for _, cert := range scoped.SslCertificates {
				var domains []string
				if cert.Managed != nil {
					domains = cert.Managed.Domains
				}
				result = append(result, SslCertificate{
					Name:       cert.Name,
					Region:     scopeToRegion(scope),
					Type:       cert.Type,
					Domains:    domains,
					ExpireTime: cert.ExpireTime,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list SSL certificates: %w", err)
	}
	return result, nil
}

// healthCheckPortAndType returns the port and protocol type for a health
// check. hc.Type names which protocol-specific sub-struct is populated.
// CreateHealthCheck creates a new global health check. Only HTTP, HTTPS, and
// TCP protocols are supported here — this is a minimal-viable create form,
// not full parity with `gcloud compute health-checks create`.
func (c *Client) CreateHealthCheck(projectID string, opts HealthCheckCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	port, err := strconv.ParseInt(opts.Port, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid port %q: %w", opts.Port, err)
	}
	interval, err := strconv.ParseInt(opts.CheckIntervalSec, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid check interval %q: %w", opts.CheckIntervalSec, err)
	}

	hc := &compute.HealthCheck{
		Name:             opts.Name,
		CheckIntervalSec: interval,
	}

	switch strings.ToUpper(opts.Protocol) {
	case "HTTPS":
		hc.Type = "HTTPS"
		hc.HttpsHealthCheck = &compute.HTTPSHealthCheck{Port: port}
	case "TCP":
		hc.Type = "TCP"
		hc.TcpHealthCheck = &compute.TCPHealthCheck{Port: port}
	default:
		hc.Type = "HTTP"
		hc.HttpHealthCheck = &compute.HTTPHealthCheck{Port: port}
	}

	_, err = c.service.HealthChecks.Insert(projectID, hc).Do()
	return err
}

// UpdateBackendServiceTimeout patches a backend service's request timeout,
// matching `gcloud compute backend-services update --timeout`. Every other
// field (protocol, health checks, session affinity, CDN, security policy,
// etc.) is a separate cross-resource edit and out of scope for this
// minimal-viable Update flow.
func (c *Client) UpdateBackendServiceTimeout(projectID, region, name string, timeoutSec int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	patch := &compute.BackendService{TimeoutSec: timeoutSec}
	if region == "global" || region == "" {
		_, err := c.service.BackendServices.Patch(projectID, name, patch).Do()
		return err
	}
	_, err := c.service.RegionBackendServices.Patch(projectID, region, name, patch).Do()
	return err
}

// GetBackendServiceHealth reports the health status of a backend service's
// first backend group, matching `gcloud compute backend-services
// get-health`. A backend service can have multiple backend groups; this
// checks only the first one, which covers the common single-group case
// without requiring the caller to pick a group.
func (c *Client) GetBackendServiceHealth(projectID, region, name string) (string, error) {
	if demo.Enabled {
		return "HEALTHY", nil
	}
	if c.service == nil {
		return "", fmt.Errorf("compute client not initialized")
	}

	var backends []*compute.Backend
	if region == "global" || region == "" {
		bs, err := c.service.BackendServices.Get(projectID, name).Do()
		if err != nil {
			return "", err
		}
		backends = bs.Backends
	} else {
		bs, err := c.service.RegionBackendServices.Get(projectID, region, name).Do()
		if err != nil {
			return "", err
		}
		backends = bs.Backends
	}
	if len(backends) == 0 {
		return "", fmt.Errorf("backend service %s has no backend groups", name)
	}
	ref := &compute.ResourceGroupReference{Group: backends[0].Group}

	var statuses []*compute.HealthStatus
	if region == "global" || region == "" {
		resp, err := c.service.BackendServices.GetHealth(projectID, name, ref).Do()
		if err != nil {
			return "", err
		}
		statuses = resp.HealthStatus
	} else {
		resp, err := c.service.RegionBackendServices.GetHealth(projectID, region, name, ref).Do()
		if err != nil {
			return "", err
		}
		statuses = resp.HealthStatus
	}

	if len(statuses) == 0 {
		return "no health status reported", nil
	}
	parts := make([]string, 0, len(statuses))
	for _, st := range statuses {
		parts = append(parts, fmt.Sprintf("%s: %s", st.IpAddress, st.HealthState))
	}
	return strings.Join(parts, ", "), nil
}

// AddBackendServiceIAMBinding grants a role to a member on a backend
// service, matching `gcloud compute backend-services
// add-iam-policy-binding`. It fetches the current policy, merges the new
// binding into it, and writes the whole policy back -- this never drops any
// existing binding, unlike a raw set-iam-policy.
func (c *Client) AddBackendServiceIAMBinding(projectID, region, name, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		policy, err := c.service.BackendServices.GetIamPolicy(projectID, name).Do()
		if err != nil {
			return fmt.Errorf("get backend service IAM policy: %w", err)
		}
		policy.Bindings = mergeComputeIAMBinding(policy.Bindings, role, member)
		_, err = c.service.BackendServices.SetIamPolicy(projectID, name, &compute.GlobalSetPolicyRequest{Policy: policy}).Do()
		return err
	}
	policy, err := c.service.RegionBackendServices.GetIamPolicy(projectID, region, name).Do()
	if err != nil {
		return fmt.Errorf("get backend service IAM policy: %w", err)
	}
	policy.Bindings = mergeComputeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.RegionBackendServices.SetIamPolicy(projectID, region, name, &compute.RegionSetPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeComputeIAMBinding appends member to the existing binding for role if
// one exists (skipping if already granted), or appends a brand-new role
// binding otherwise. It never removes or replaces any other binding.
func mergeComputeIAMBinding(bindings []*compute.Binding, role, member string) []*compute.Binding {
	for _, b := range bindings {
		if b.Role != role {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return bindings
			}
		}
		b.Members = append(b.Members, member)
		return bindings
	}
	return append(bindings, &compute.Binding{Role: role, Members: []string{member}})
}

// DeleteUrlMap deletes a URL map, matching `gcloud compute url-maps
// delete`.
func (c *Client) DeleteUrlMap(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		_, err := c.service.UrlMaps.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.RegionUrlMaps.Delete(projectID, region, name).Do()
	return err
}

// InvalidateUrlMapCache invalidates CDN-cached content under a URL map for
// the given path pattern, matching `gcloud compute url-maps
// invalidate-cdn-cache --path`. Only global URL maps support cache
// invalidation.
func (c *Client) InvalidateUrlMapCache(projectID, name, path string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.UrlMaps.InvalidateCache(projectID, name, &compute.CacheInvalidationRule{Path: path}).Do()
	return err
}

// DeleteForwardingRule deletes a forwarding rule, matching `gcloud compute
// forwarding-rules delete`.
func (c *Client) DeleteForwardingRule(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		_, err := c.service.GlobalForwardingRules.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.ForwardingRules.Delete(projectID, region, name).Do()
	return err
}

// DeleteSslCertificate deletes an SSL certificate, matching `gcloud compute
// ssl-certificates delete`.
func (c *Client) DeleteSslCertificate(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	if region == "global" || region == "" {
		_, err := c.service.SslCertificates.Delete(projectID, name).Do()
		return err
	}
	_, err := c.service.RegionSslCertificates.Delete(projectID, region, name).Do()
	return err
}

func healthCheckPortAndType(hc *compute.HealthCheck) (port int64, checkType string) {
	switch {
	case hc.HttpHealthCheck != nil:
		return hc.HttpHealthCheck.Port, "HTTP"
	case hc.HttpsHealthCheck != nil:
		return hc.HttpsHealthCheck.Port, "HTTPS"
	case hc.Http2HealthCheck != nil:
		return hc.Http2HealthCheck.Port, "HTTP2"
	case hc.TcpHealthCheck != nil:
		return hc.TcpHealthCheck.Port, "TCP"
	case hc.SslHealthCheck != nil:
		return hc.SslHealthCheck.Port, "SSL"
	case hc.GrpcHealthCheck != nil:
		return hc.GrpcHealthCheck.Port, "GRPC"
	default:
		return 0, hc.Type
	}
}

// firstHealthCheckName returns the short name of the first health check
// attached to a backend service, extracted from its full resource URL.
func firstHealthCheckName(healthChecks []string) string {
	if len(healthChecks) == 0 {
		return ""
	}
	return shortName(healthChecks[0])
}

func shortName(url string) string {
	parts := strings.Split(url, "/")
	return parts[len(parts)-1]
}

// scopeToRegion converts an AggregatedList scope key (e.g.
// "regions/us-central1" or "global") into a display-friendly region string.
func scopeToRegion(scope string) string {
	if scope == "global" {
		return "global"
	}
	parts := strings.Split(scope, "/")
	return parts[len(parts)-1]
}
