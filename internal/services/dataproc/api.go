package dataproc

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/dataproc/v1"
)

type Client struct {
	service *dataproc.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := dataproc.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("dataproc client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListClusters(projectID string, region string) ([]Cluster, error) {
	if demo.Enabled {
		return []Cluster{}, nil
	}
	var clusters []Cluster

	err := c.service.Projects.Regions.Clusters.List(projectID, region).Pages(context.Background(), func(page *dataproc.ListClustersResponse) error {
		for _, cl := range page.Clusters {
			status := "UNKNOWN"
			statusDetail := ""
			stateStartTime := ""
			if cl.Status != nil {
				status = cl.Status.State
				statusDetail = cl.Status.Detail
				stateStartTime = cl.Status.StateStartTime
			}

			configBucket := ""

			masterType := "N/A"
			workerType := "N/A"
			workerCount := 0
			zone := ""

			if cl.Config != nil {
				if cl.Config.MasterConfig != nil {
					masterType = machineTypeShort(cl.Config.MasterConfig.MachineTypeUri)
				}
				if cl.Config.WorkerConfig != nil {
					workerType = machineTypeShort(cl.Config.WorkerConfig.MachineTypeUri)
					workerCount = int(cl.Config.WorkerConfig.NumInstances)
				}
				if cl.Config.GceClusterConfig != nil {
					zone = machineTypeShort(cl.Config.GceClusterConfig.ZoneUri) // Reuse shortener for zone
				}
				configBucket = cl.Config.ConfigBucket
			}

			clusters = append(clusters, Cluster{
				Name:           cl.ClusterName,
				ProjectID:      projectID,
				Status:         status,
				MasterMachine:  masterType,
				WorkerCount:    workerCount,
				WorkerMachine:  workerType,
				Zone:           zone,
				ClusterUUID:    cl.ClusterUuid,
				StatusDetail:   statusDetail,
				StateStartTime: stateStartTime,
				ConfigBucket:   configBucket,
				Labels:         cl.Labels,
			})
		}
		return nil
	})
	return clusters, err
}

// CreateCluster creates a new Dataproc cluster in the given region.
func (c *Client) CreateCluster(projectID, region, clusterName, zone, masterMachineType, workerMachineType string, numWorkers int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}

	config := &dataproc.ClusterConfig{
		MasterConfig: &dataproc.InstanceGroupConfig{
			NumInstances:   1,
			MachineTypeUri: masterMachineType,
		},
		WorkerConfig: &dataproc.InstanceGroupConfig{
			NumInstances:   int64(numWorkers),
			MachineTypeUri: workerMachineType,
		},
	}
	if zone != "" {
		config.GceClusterConfig = &dataproc.GceClusterConfig{
			ZoneUri: zone,
		}
	}

	cluster := &dataproc.Cluster{
		ClusterName: clusterName,
		ProjectId:   projectID,
		Config:      config,
	}

	_, err := c.service.Projects.Regions.Clusters.Create(projectID, region, cluster).Do()
	return err
}

// UpdateClusterWorkerCount patches the primary worker-group node count of an
// existing cluster, matching `gcloud dataproc clusters update --num-workers`.
// Secondary/preemptible worker groups, autoscaling policies, and graceful
// decommission timeouts are out of scope for this minimal Update flow.
func (c *Client) UpdateClusterWorkerCount(projectID, region, clusterName string, numWorkers int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	cluster := &dataproc.Cluster{
		ClusterName: clusterName,
		ProjectId:   projectID,
		Config: &dataproc.ClusterConfig{
			WorkerConfig: &dataproc.InstanceGroupConfig{
				NumInstances: int64(numWorkers),
			},
		},
	}
	_, err := c.service.Projects.Regions.Clusters.Patch(projectID, region, clusterName, cluster).
		UpdateMask("config.worker_config.num_instances").Do()
	return err
}

// DeleteCluster deletes a Dataproc cluster, matching
// `gcloud dataproc clusters delete`.
func (c *Client) DeleteCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Delete(projectID, region, clusterName).Do()
	return err
}

// StartCluster starts a stopped Dataproc cluster, matching
// `gcloud dataproc clusters start`.
func (c *Client) StartCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Start(projectID, region, clusterName, &dataproc.StartClusterRequest{}).Do()
	return err
}

// StopCluster stops a running Dataproc cluster, matching
// `gcloud dataproc clusters stop`. `diagnose` (collecting a diagnostic
// tarball) is a separate, higher-complexity operation with async output
// handling and is intentionally out of scope for this pass.
func (c *Client) StopCluster(projectID, region, clusterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataproc client not initialized")
	}
	_, err := c.service.Projects.Regions.Clusters.Stop(projectID, region, clusterName, &dataproc.StopClusterRequest{}).Do()
	return err
}

// machineTypeShort extracts "n1-standard-4" from full URI
func machineTypeShort(uri string) string {
	// .../zones/us-central1-a/machineTypes/n1-standard-4
	// or .../zones/us-central1-a
	parts := parseURI(uri) // Simplified check
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return uri
}

func parseURI(uri string) []string {
	// Simple splitter
	return strings.Split(uri, "/")
}
