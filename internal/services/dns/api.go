package dns

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	gdns "google.golang.org/api/dns/v1"
)

// Client wraps the Cloud DNS API.
//
// Cloud DNS has no dedicated cloud.google.com/go client module (unlike
// Cloud Scheduler/Cloud Tasks) — only the older REST-generated
// google.golang.org/api/dns/v1 package exists, so that's what this client
// uses. This mirrors the fallback the Parameter Manager service took in the
// prior round for the same reason.
type Client struct {
	service *gdns.Service
}

// NewClient creates a new Cloud DNS client.
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := gdns.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("dns client: %w", err)
	}
	return &Client{service: svc}, nil
}

// ListZones lists all Cloud DNS managed zones in the project. Cloud DNS
// managed zones are project-scoped (not region-scoped), so a single List
// call — paginated — covers every zone in the project.
func (c *Client) ListZones(projectID string) ([]Zone, error) {
	if demo.Enabled {
		return []Zone{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not init")
	}

	var zones []Zone
	err := c.service.ManagedZones.List(projectID).Pages(context.Background(), func(resp *gdns.ManagedZonesListResponse) error {
		for _, z := range resp.ManagedZones {
			zones = append(zones, toZone(z))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list managed zones: %w", err)
	}
	return zones, nil
}

// ListRecordSets lists all resource record sets in a managed zone.
func (c *Client) ListRecordSets(projectID, zoneName string) ([]RecordSet, error) {
	if demo.Enabled {
		return []RecordSet{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not init")
	}

	var records []RecordSet
	err := c.service.ResourceRecordSets.List(projectID, zoneName).Pages(context.Background(), func(resp *gdns.ResourceRecordSetsListResponse) error {
		for _, r := range resp.Rrsets {
			records = append(records, RecordSet{
				Name:    r.Name,
				Type:    r.Type,
				TTL:     r.Ttl,
				Rrdatas: r.Rrdatas,
			})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list record sets in %s: %w", zoneName, err)
	}
	return records, nil
}

// CreateZone creates a new Cloud DNS managed zone. visibility is either
// "public" or "private"; anything else falls back to "public".
func (c *Client) CreateZone(projectID, name, dnsName, description, visibility string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dns client not initialized")
	}
	visibility = strings.ToLower(strings.TrimSpace(visibility))
	if visibility != "private" {
		visibility = "public"
	}
	zone := &gdns.ManagedZone{
		Name:        name,
		DnsName:     dnsName,
		Description: description,
		Visibility:  visibility,
	}
	_, err := c.service.ManagedZones.Create(projectID, zone).Do()
	return err
}

// DeleteZone deletes a Cloud DNS managed zone, matching
// `gcloud dns managed-zones delete`. The API itself refuses to delete a
// zone that still has any record sets beyond the default NS/SOA pair, which
// is the safety behavior this app relies on (no record-set delete is
// implemented here to empty it first).
func (c *Client) DeleteZone(projectID, zoneName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dns client not initialized")
	}
	return c.service.ManagedZones.Delete(projectID, zoneName).Do()
}

func toZone(z *gdns.ManagedZone) Zone {
	visibility := strings.ToUpper(z.Visibility)
	if visibility == "" {
		visibility = "PUBLIC"
	}
	return Zone{
		Name:        z.Name,
		DNSName:     z.DnsName,
		Visibility:  visibility,
		Description: z.Description,
		CreateTime:  z.CreationTime,
	}
}
