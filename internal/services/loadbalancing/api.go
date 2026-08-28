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
