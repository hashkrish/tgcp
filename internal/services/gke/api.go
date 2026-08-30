package gke

import (
	"context"
	"fmt"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/container/v1"
	"google.golang.org/api/option"
)

type Client struct {
	service *container.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := container.NewService(ctx, option.WithScopes(container.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("gke client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListClusters(projectID string) ([]Cluster, error) {
	if demo.Enabled {
		return []Cluster{}, nil
	}
	// Use aggregated list to get clusters from all zones/regions
	parent := fmt.Sprintf("projects/%s/locations/-", projectID)
	resp, err := c.service.Projects.Locations.Clusters.List(parent).Do()
	if err != nil {
		return nil, err
	}

	var clusters []Cluster
	for _, cl := range resp.Clusters {
		// Parse location from selflink or name if needed,
		// but List response struct usually has Location field if we used parent with location "-"
		// Actually for aggregated list we usually use projects/{projectId}/locations/-
		// Let's verify if the above List call supports "-"

		clusters = append(clusters, Cluster{
			Name:          cl.Name,
			Location:      cl.Location,
			Status:        cl.Status,
			MasterVersion: cl.CurrentMasterVersion,
			Endpoint:      cl.Endpoint,
			Network:       cl.Network,
			Subnetwork:    cl.Subnetwork,
			NodeCount:     int(cl.CurrentNodeCount),
			Mode:          getMode(cl),
			SelfLink:      cl.SelfLink,
			NodePools:     convertNodePools(cl.NodePools),
		})
	}
	return clusters, nil
}

// CreateCluster creates a new Standard-mode GKE cluster with a single
// default node pool. This intentionally omits most of the Cluster resource's
// many optional fields (networking, addons, autoscaling, release channel,
// Workload Identity, etc.) — only InitialNodeCount and NodeConfig.MachineType
// are set, matching the minimal viable field set for this Create flow.
func (c *Client) CreateCluster(projectID, location, name string, nodeCount int64, machineType string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("gke client not initialized")
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, location)
	req := &container.CreateClusterRequest{
		Cluster: &container.Cluster{
			Name:             name,
			InitialNodeCount: nodeCount,
			NodeConfig: &container.NodeConfig{
				MachineType: machineType,
			},
		},
	}

	_, err := c.service.Projects.Locations.Clusters.Create(parent, req).Do()
	return err
}

// ResizeNodePool changes the node count of an existing node pool, matching
// `gcloud container clusters resize --node-pool`. See UpgradeMaster/
// UpgradeNodePool below for version upgrades; `complete-control-plane-upgrade`
// remains out of scope.
func (c *Client) ResizeNodePool(projectID, location, clusterName, nodePoolName string, nodeCount int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("gke client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, location, clusterName, nodePoolName)
	req := &container.SetNodePoolSizeRequest{
		NodeCount: nodeCount,
	}
	_, err := c.service.Projects.Locations.Clusters.NodePools.SetSize(name, req).Do()
	return err
}

// UpgradeMaster changes the cluster's control-plane (master) Kubernetes
// version, matching `gcloud container clusters upgrade --master
// --cluster-version=VERSION`. version accepts an explicit version or an
// alias like "latest" (see UpdateMasterRequest.MasterVersion).
func (c *Client) UpgradeMaster(projectID, location, clusterName, version string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("gke client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, clusterName)
	req := &container.UpdateMasterRequest{MasterVersion: version}
	_, err := c.service.Projects.Locations.Clusters.UpdateMaster(name, req).Do()
	return err
}

// UpgradeNodePool changes a node pool's Kubernetes version, matching
// `gcloud container clusters upgrade --node-pool=POOL
// --cluster-version=VERSION`. Every other node-pool setting (machine type,
// image, autoscaling, etc.) is left untouched.
func (c *Client) UpgradeNodePool(projectID, location, clusterName, nodePoolName, version string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("gke client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/clusters/%s/nodePools/%s", projectID, location, clusterName, nodePoolName)
	req := &container.UpdateNodePoolRequest{NodeVersion: version}
	_, err := c.service.Projects.Locations.Clusters.NodePools.Update(name, req).Do()
	return err
}

// DeleteCluster deletes an entire GKE cluster, matching
// `gcloud container clusters delete`. This is one of the most destructive
// operations in this app (irreversibly destroys every node pool and workload
// in the cluster), so callers must require a double confirmation before
// invoking this.
func (c *Client) DeleteCluster(projectID, location, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("gke client not initialized")
	}
	fullName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", projectID, location, name)
	_, err := c.service.Projects.Locations.Clusters.Delete(fullName).Do()
	return err
}

// Helpers

func getMode(cl *container.Cluster) string {
	if cl.Autopilot != nil && cl.Autopilot.Enabled {
		return "Autopilot"
	}
	return "Standard"
}

func convertNodePools(apiPools []*container.NodePool) []NodePool {
	var pools []NodePool
	for _, p := range apiPools {
		var machineType string
		var diskSize int64
		var isSpot bool
		var minCount, maxCount int64

		if p.Config != nil {
			machineType = p.Config.MachineType
			diskSize = p.Config.DiskSizeGb
			isSpot = p.Config.Spot
		}

		if p.Autoscaling != nil {
			minCount = p.Autoscaling.MinNodeCount
			maxCount = p.Autoscaling.MaxNodeCount
		}

		pools = append(pools, NodePool{
			Name:             p.Name,
			Status:           p.Status,
			MachineType:      machineType,
			DiskSizeGb:       diskSize,
			InitialNodeCount: p.InitialNodeCount,
			Autoscaling: AutoscalingConfig{
				Enabled:      p.Autoscaling != nil && p.Autoscaling.Enabled,
				MinNodeCount: minCount,
				MaxNodeCount: maxCount,
			},
			IsSpot:  isSpot,
			Version: p.Version,
		})
	}
	return pools
}
