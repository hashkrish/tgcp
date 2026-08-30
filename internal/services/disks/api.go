package disks

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	svc, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("compute client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListDisks(projectID string) ([]Disk, error) {
	if demo.Enabled {
		return []Disk{}, nil
	}
	// Use aggregated list to get disks from all zones
	req := c.service.Disks.AggregatedList(projectID)
	var disks []Disk

	if err := req.Pages(context.Background(), func(page *compute.DiskAggregatedList) error {
		for _, scopedList := range page.Items {
			for _, d := range scopedList.Disks {
				// Parse Zone from URL: https://www.googleapis.com/compute/v1/projects/.../zones/us-central1-a
				zone := ""
				parts := strings.Split(d.Zone, "/")
				if len(parts) > 0 {
					zone = parts[len(parts)-1]
				}

				disks = append(disks, Disk{
					Name:                d.Name,
					Zone:                zone,
					SizeGb:              d.SizeGb,
					Type:                d.Type,
					Status:              d.Status,
					LastAttachTimestamp: d.LastAttachTimestamp,
					Users:               d.Users,
					SourceImage:         d.SourceImage,
				})
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}

	return disks, nil
}

// CreateSnapshot creates a snapshot of the given disk in the given zone.
// The snapshot name is derived from the disk name plus a timestamp to avoid collisions.
func (c *Client) CreateSnapshot(projectID, zone, diskName string) (string, error) {
	snapshotName := fmt.Sprintf("%s-snap-%s", diskName, time.Now().Format("20060102-150405"))

	if demo.Enabled {
		return snapshotName, nil
	}

	if c.service == nil {
		return "", fmt.Errorf("compute client not initialized")
	}

	req := &compute.Snapshot{
		Name: snapshotName,
	}

	_, err := c.service.Disks.CreateSnapshot(projectID, zone, diskName, req).Do()
	if err != nil {
		return "", err
	}

	return snapshotName, nil
}

// DeleteDisk deletes a standalone persistent disk, matching
// `gcloud compute disks delete`. The Compute API itself refuses to delete a
// disk that is still attached to an instance.
func (c *Client) DeleteDisk(projectID, zone, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Disks.Delete(projectID, zone, name).Do()
	return err
}

// CreateDisk creates a new standalone persistent disk in the given zone.
func (c *Client) CreateDisk(projectID, zone, name string, sizeGB int64, diskType string) error {
	if demo.Enabled {
		return nil
	}

	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}

	req := &compute.Disk{
		Name:   name,
		SizeGb: sizeGB,
		Type:   fmt.Sprintf("zones/%s/diskTypes/%s", zone, diskType),
	}

	_, err := c.service.Disks.Insert(projectID, zone, req).Do()
	return err
}

// ResizeDisk grows a persistent disk to newSizeGB. This is the minimal
// viable Update for Disks — the Compute API only supports growing a disk,
// never shrinking, matching `gcloud compute disks resize`. Move and
// update-kms-key are separate, higher-risk operations and are intentionally
// out of scope.
func (c *Client) ResizeDisk(projectID, zone, name string, newSizeGB int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	req := &compute.DisksResizeRequest{
		SizeGb: newSizeGB,
	}
	_, err := c.service.Disks.Resize(projectID, zone, name, req).Do()
	return err
}

// StartAsyncReplication begins async disk replication from this disk to
// secondaryDiskURI (a full or partial resource URL to the secondary disk),
// matching `gcloud compute disks start-async-replication
// --secondary-disk=SECONDARY_DISK_URI`.
func (c *Client) StartAsyncReplication(projectID, zone, name, secondaryDiskURI string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	req := &compute.DisksStartAsyncReplicationRequest{
		AsyncSecondaryDisk: secondaryDiskURI,
	}
	_, err := c.service.Disks.StartAsyncReplication(projectID, zone, name, req).Do()
	return err
}

// StopAsyncReplication stops async disk replication out of this disk,
// matching `gcloud compute disks stop-async-replication`.
func (c *Client) StopAsyncReplication(projectID, zone, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	_, err := c.service.Disks.StopAsyncReplication(projectID, zone, name).Do()
	return err
}

// GetDiskIAMPolicy reads a disk's current IAM policy, matching `gcloud
// compute disks get-iam-policy`. Used as the "look before you grant" read
// step before AddDiskIAMBinding.
func (c *Client) GetDiskIAMPolicy(projectID, zone, name string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("compute client not initialized")
	}
	policy, err := c.service.Disks.GetIamPolicy(projectID, zone, name).Do()
	if err != nil {
		return nil, fmt.Errorf("get disk IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddDiskIAMBinding grants role to member on a disk, matching `gcloud
// compute disks add-iam-policy-binding`. Fetches the current policy, merges
// the binding in, and writes the whole policy back — this never drops any
// existing binding, unlike a raw set-iam-policy.
func (c *Client) AddDiskIAMBinding(projectID, zone, name, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("compute client not initialized")
	}
	policy, err := c.service.Disks.GetIamPolicy(projectID, zone, name).Do()
	if err != nil {
		return fmt.Errorf("get disk IAM policy: %w", err)
	}
	found := false
	for _, b := range policy.Bindings {
		if b.Role != role {
			continue
		}
		found = true
		for _, m := range b.Members {
			if m == member {
				return nil // already granted, nothing to do
			}
		}
		b.Members = append(b.Members, member)
		break
	}
	if !found {
		policy.Bindings = append(policy.Bindings, &compute.Binding{Role: role, Members: []string{member}})
	}
	_, err = c.service.Disks.SetIamPolicy(projectID, zone, name, &compute.ZoneSetPolicyRequest{Policy: policy}).Do()
	return err
}
