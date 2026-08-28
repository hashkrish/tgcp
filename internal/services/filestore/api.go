package filestore

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gfilestore "cloud.google.com/go/filestore/apiv1"
	"cloud.google.com/go/filestore/apiv1/filestorepb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// Client wraps the Cloud Filestore Manager API client.
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

// CreateInstance creates a new Filestore instance in the given zone/region
// with a single file share and a single network attachment — this is a
// minimal-viable create form, not full parity with `gcloud filestore
// instances create`.
func (c *Client) CreateInstance(projectID string, opts InstanceCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not init")
	}

	capacity, err := strconv.ParseInt(opts.CapacityGB, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid capacity %q: %w", opts.CapacityGB, err)
	}

	tier, ok := filestorepb.Instance_Tier_value[strings.ToUpper(opts.Tier)]
	if !ok {
		return fmt.Errorf("invalid tier %q", opts.Tier)
	}

	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, opts.Zone)

	req := &filestorepb.CreateInstanceRequest{
		Parent:     parent,
		InstanceId: opts.InstanceID,
		Instance: &filestorepb.Instance{
			Tier: filestorepb.Instance_Tier(tier),
			FileShares: []*filestorepb.FileShareConfig{
				{Name: opts.ShareName, CapacityGb: capacity},
			},
			Networks: []*filestorepb.NetworkConfig{
				{Network: opts.Network},
			},
		},
	}

	// Fire-and-forget: this is a long-running operation; don't block on it
	// completing — the next list refresh will reflect it once done.
	_, err = c.client.CreateInstance(ctx, req)
	return err
}

// UpdateInstanceCapacity resizes an instance's first (and, for the create
// flow this app offers, only) file share, matching `gcloud filestore
// instances update --file-share=name=...,capacity=...`. Multi-share
// instances only have their first share resized here; revert and
// promote/pause/resume-replica are separate, higher-risk operations and are
// out of scope for this minimal Update flow.
func (c *Client) UpdateInstanceCapacity(fullName, shareName string, capacityGB int64) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not init")
	}
	req := &filestorepb.UpdateInstanceRequest{
		Instance: &filestorepb.Instance{
			Name: fullName,
			FileShares: []*filestorepb.FileShareConfig{
				{Name: shareName, CapacityGb: capacityGB},
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"file_shares"}},
	}
	_, err := c.client.UpdateInstance(context.Background(), req)
	return err
}

// DeleteInstance deletes a Filestore instance, matching
// `gcloud filestore instances delete`. This permanently destroys all file
// shares (and their data) on the instance.
func (c *Client) DeleteInstance(fullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not init")
	}
	_, err := c.client.DeleteInstance(context.Background(), &filestorepb.DeleteInstanceRequest{Name: fullName})
	return err
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
		FullName:      i.GetName(),
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
