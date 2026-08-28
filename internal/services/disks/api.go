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
