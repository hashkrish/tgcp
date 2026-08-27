package filestore

import (
	"context"
	"fmt"
	"strings"

	gfilestore "cloud.google.com/go/filestore/apiv1"
	"cloud.google.com/go/filestore/apiv1/filestorepb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"
)

// Client wraps the Cloud Filestore Manager API client. Read-only: only List*
// calls are ever made — no create/update/delete/restore operations.
type Client struct {
	client *gfilestore.CloudFilestoreManagerClient
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := gfilestore.NewCloudFilestoreManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("filestore client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListInstances lists Filestore instances across every location the project
// has instances in. Filestore instances are zone-scoped (BASIC tiers) or
// region-scoped (other tiers), and ListInstances requires a parent of the
// form projects/{project}/locations/{location} (the API does document a "-"
// wildcard for "all locations", but we deliberately mirror the
// scheduler/cloudtasks pattern used elsewhere in this codebase instead: we
// discover the project's available locations via the Cloud Locations API
// and fan out one ListInstances call per location. This keeps the region
// discovery logic consistent across services and avoids hardcoding any
// region/zone list.
func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		return []Instance{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()

	locations, err := c.listLocationIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var instances []Instance
	for _, loc := range locations {
		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
		it := c.client.ListInstances(ctx, &filestorepb.ListInstancesRequest{Parent: parent})
		for inst, err := range it.All() {
			if err != nil {
				return nil, fmt.Errorf("list filestore instances in %s: %w", loc, err)
			}
			instances = append(instances, toInstance(inst, loc))
		}
	}
	return instances, nil
}

// listLocationIDs returns the canonical location IDs (zones for BASIC tier
// instances, regions for the rest) available to Filestore for this project.
func (c *Client) listLocationIDs(ctx context.Context, projectID string) ([]string, error) {
	var ids []string
	req := &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}
	it := c.client.ListLocations(ctx, req)
	for loc, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list filestore locations: %w", err)
		}
		ids = append(ids, loc.LocationId)
	}
	return ids, nil
}

func toInstance(i *filestorepb.Instance, location string) Instance {
	var shares []FileShare
	var totalCapacity int64
	for _, fs := range i.GetFileShares() {
		shares = append(shares, FileShare{
			Name:       fs.GetName(),
			CapacityGB: fs.GetCapacityGb(),
		})
		totalCapacity += fs.GetCapacityGb()
	}

	var network string
	var ipAddresses []string
	if nets := i.GetNetworks(); len(nets) > 0 {
		network = nets[0].GetNetwork()
		ipAddresses = nets[0].GetIpAddresses()
	}

	createTime := ""
	if t := i.GetCreateTime(); t != nil {
		createTime = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}

	return Instance{
		Name:          shortName(i.GetName()),
		Location:      location,
		Tier:          i.GetTier().String(),
		State:         i.GetState().String(),
		StatusMessage: i.GetStatusMessage(),
		CapacityGB:    totalCapacity,
		Network:       network,
		IPAddresses:   ipAddresses,
		CreateTime:    createTime,
		FileShares:    shares,
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}
