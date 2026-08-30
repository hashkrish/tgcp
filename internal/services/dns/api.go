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

// UpdateZoneDescription patches a zone's description, matching
// `gcloud dns managed-zones update --description`. DNSSEC config and other
// zone fields are out of scope for this minimal Update flow.
func (c *Client) UpdateZoneDescription(projectID, zoneName, description string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dns client not initialized")
	}
	_, err := c.service.ManagedZones.Patch(projectID, zoneName, &gdns.ManagedZone{Description: description}).Do()
	return err
}

// GetZoneIAMPolicy reads a zone's current IAM policy, matching
// `gcloud dns managed-zones get-iam-policy`. Used as the "look before you
// grant" read step before AddZoneIAMBinding.
func (c *Client) GetZoneIAMPolicy(projectID, zoneName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("dns client not initialized")
	}
	policy, err := c.service.ManagedZones.GetIamPolicy(zoneName, &gdns.GoogleIamV1GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, fmt.Errorf("get zone IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddZoneIAMBinding grants a role to a member on a zone, matching
// `gcloud dns managed-zones add-iam-policy-binding`. It fetches the
// current policy, merges the new binding into it, and writes the whole
// policy back -- this never drops any existing binding, unlike a raw
// set-iam-policy.
func (c *Client) AddZoneIAMBinding(projectID, zoneName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dns client not initialized")
	}
	policy, err := c.service.ManagedZones.GetIamPolicy(zoneName, &gdns.GoogleIamV1GetIamPolicyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("get zone IAM policy: %w", err)
	}
	found := false
	for _, b := range policy.Bindings {
		if b.Role != role {
			continue
		}
		found = true
		alreadyMember := false
		for _, m := range b.Members {
			if m == member {
				alreadyMember = true
				break
			}
		}
		if !alreadyMember {
			b.Members = append(b.Members, member)
		}
		break
	}
	if !found {
		policy.Bindings = append(policy.Bindings, &gdns.GoogleIamV1Binding{Role: role, Members: []string{member}})
	}
	_, err = c.service.ManagedZones.SetIamPolicy(zoneName, &gdns.GoogleIamV1SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// UpdateRecordSet replaces an existing record set's TTL and rrdata,
// matching `gcloud dns record-sets update`. name/recordType identify the
// existing record; the API requires the full replacement record shape
// (there's no partial-field patch), so both TTL and rrdatas must be
// supplied.
func (c *Client) UpdateRecordSet(projectID, zoneName, name, recordType string, ttl int64, rrdatas []string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dns client not initialized")
	}
	rrset := &gdns.ResourceRecordSet{
		Name:    name,
		Type:    recordType,
		Ttl:     ttl,
		Rrdatas: rrdatas,
	}
	_, err := c.service.ResourceRecordSets.Patch(projectID, zoneName, name, recordType, rrset).Do()
	return err
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
