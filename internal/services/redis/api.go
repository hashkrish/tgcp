package redis

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/redis/v1"
)

type Client struct {
	service *redis.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := redis.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("redis client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		return []Instance{}, nil
	}
	var instances []Instance
	// Aggregated list works best to find across regions
	parent := fmt.Sprintf("projects/%s/locations/-", projectID)

	err := c.service.Projects.Locations.Instances.List(parent).Pages(context.Background(), func(page *redis.ListInstancesResponse) error {
		for _, i := range page.Instances {
			// Name format: projects/{project}/locations/{location}/instances/{instance_id}
			parts := strings.Split(i.Name, "/")
			shortName := parts[len(parts)-1]
			location := ""
			if len(parts) > 3 {
				location = parts[len(parts)-3]
			}

			// Network: projects/{project}/global/networks/{network}
			netParts := strings.Split(i.AuthorizedNetwork, "/")
			network := i.AuthorizedNetwork
			if len(netParts) > 0 {
				network = netParts[len(netParts)-1]
			}

			instances = append(instances, Instance{
				Name:               shortName,
				DisplayName:        i.DisplayName,
				ProjectID:          projectID,
				Location:           location,
				LocationID:         i.LocationId,
				CurrentLocationID:  i.CurrentLocationId,
				Tier:               i.Tier,
				MemorySizeGb:       int(i.MemorySizeGb),
				RedisVersion:       i.RedisVersion,
				Host:               i.Host,
				Port:               int(i.Port),
				ReadEndpoint:       i.ReadEndpoint,
				ReadEndpointPort:   int(i.ReadEndpointPort),
				State:              i.State,
				StatusMessage:      i.StatusMessage,
				CreateTime:         i.CreateTime,
				AuthorizedNetwork:  network,
				ConnectMode:        i.ConnectMode,
				ReservedIPRange:    i.ReservedIpRange,
				TransitEncryption:  i.TransitEncryptionMode,
				AuthEnabled:        i.AuthEnabled,
				ReplicaCount:       int(i.ReplicaCount),
				ReadReplicasMode:   i.ReadReplicasMode,
				PersistenceMode:    persistenceMode(i.PersistenceConfig),
				CustomerManagedKey: i.CustomerManagedKey,
				Labels:             i.Labels,
			})
		}
		return nil
	})
	return instances, err
}

// CreateInstance creates a new Memorystore Redis instance in the given region.
func (c *Client) CreateInstance(projectID, instanceID, region, tier string, memoryGb int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("redis client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	instance := &redis.Instance{
		Tier:         tier,
		MemorySizeGb: memoryGb,
	}
	_, err := c.service.Projects.Locations.Instances.Create(parent, instance).InstanceId(instanceID).Do()
	return err
}

// UpdateInstanceMemorySize patches the memory size (GB) of an existing
// Memorystore Redis instance. This is the simplest single-field Update
// operation for Redis; a full Redis version "upgrade" requires a separate
// UpgradeInstance RPC (an async, higher-risk operation) and is intentionally
// not implemented here.
func (c *Client) UpdateInstanceMemorySize(projectID, instanceID, region string, memoryGb int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("redis client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, region, instanceID)
	instance := &redis.Instance{
		MemorySizeGb: memoryGb,
	}
	_, err := c.service.Projects.Locations.Instances.Patch(name, instance).UpdateMask("memorySizeGb").Do()
	return err
}

// FailoverInstance promotes the current read replica to primary for a
// STANDARD_HA-tier instance, matching `gcloud redis instances failover`.
// Always uses LIMITED_DATA_LOSS (the gcloud default) rather than
// FORCE_DATA_LOSS, which risks losing unreplicated writes.
// reschedule-maintenance is a separate, narrower RPC and is out of scope.
func (c *Client) FailoverInstance(projectID, instanceID, region string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("redis client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, region, instanceID)
	_, err := c.service.Projects.Locations.Instances.Failover(name, &redis.FailoverInstanceRequest{
		DataProtectionMode: "LIMITED_DATA_LOSS",
	}).Do()
	return err
}

// DeleteInstance deletes a Memorystore Redis instance, matching
// `gcloud redis instances delete`.
func (c *Client) DeleteInstance(projectID, instanceID, region string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("redis client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, region, instanceID)
	_, err := c.service.Projects.Locations.Instances.Delete(name).Do()
	return err
}

func persistenceMode(cfg *redis.PersistenceConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.PersistenceMode
}
