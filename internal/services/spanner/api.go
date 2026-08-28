package spanner

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/spanner/v1"
)

type Client struct {
	service *spanner.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := spanner.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("spanner client: %w", err)
	}
	return &Client{service: svc}, nil
}

// CreateInstance creates a new Spanner instance with the given ID, display
// name, instance config (e.g. "regional-us-central1"), and node count.
func (c *Client) CreateInstance(projectID, instanceID, displayName, config string, nodeCount int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("spanner client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", projectID)
	req := &spanner.CreateInstanceRequest{
		InstanceId: instanceID,
		Instance: &spanner.Instance{
			Config:      fmt.Sprintf("projects/%s/instanceConfigs/%s", projectID, config),
			DisplayName: displayName,
			NodeCount:   int64(nodeCount),
		},
	}
	_, err := c.service.Projects.Instances.Create(parent, req).Do()
	return err
}

// UpdateInstanceNodeCount patches the node count of an existing Spanner
// instance, matching `gcloud spanner instances update --nodes`. DDL update
// and instance `move` are separate operations and are intentionally out of
// scope.
func (c *Client) UpdateInstanceNodeCount(projectID, instanceID string, nodeCount int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("spanner client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	req := &spanner.UpdateInstanceRequest{
		Instance: &spanner.Instance{
			Name:      name,
			NodeCount: int64(nodeCount),
		},
		FieldMask: "nodeCount",
	}
	_, err := c.service.Projects.Instances.Patch(name, req).Do()
	return err
}

// DeleteInstance deletes a Spanner instance and all its databases, matching
// `gcloud spanner instances delete`.
func (c *Client) DeleteInstance(projectID, instanceID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("spanner client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	_, err := c.service.Projects.Instances.Delete(name).Do()
	return err
}

func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		return []Instance{}, nil
	}
	var instances []Instance
	parent := fmt.Sprintf("projects/%s", projectID)

	err := c.service.Projects.Instances.List(parent).Pages(context.Background(), func(page *spanner.ListInstancesResponse) error {
		for _, i := range page.Instances {
			// Name: projects/{project}/instances/{instance}
			parts := strings.Split(i.Name, "/")
			shortName := parts[len(parts)-1]

			// Config: projects/{project}/instanceConfigs/{config}
			configParts := strings.Split(i.Config, "/")
			shortConfig := configParts[len(configParts)-1]

			instances = append(instances, Instance{
				Name:                      shortName,
				DisplayName:               i.DisplayName,
				ProjectID:                 projectID,
				Config:                    shortConfig,
				State:                     i.State,
				NodeCount:                 int(i.NodeCount),
				ProcessingUnits:           int(i.ProcessingUnits),
				Labels:                    i.Labels,
				Edition:                   i.Edition,
				DefaultBackupScheduleType: i.DefaultBackupScheduleType,
				CreateTime:                i.CreateTime,
				UpdateTime:                i.UpdateTime,
			})
		}
		return nil
	})
	return instances, err
}
