// Package loadbalancing provides a read-only view of Cloud Load Balancing
// resources, scoped deliberately to the minimum useful slice: Backend
// Services and Health Checks. URL maps, forwarding rules, and SSL
// certificates are NOT covered here — see the package-level comment in
// loadbalancing.go for the reasoning.
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

// healthCheckPortAndType returns the port and protocol type for a health
// check. hc.Type names which protocol-specific sub-struct is populated.
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
