package bigtable

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/bigtableadmin/v2"
)

type Client struct {
	service *bigtableadmin.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := bigtableadmin.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("bigtable client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		var fixtures []Instance
		demo.MustLoad("bigtable", &fixtures)
		return fixtures, nil
	}
	var instances []Instance
	parent := fmt.Sprintf("projects/%s", projectID)

	// Note: Bigtable API handles pagination, but for MVP we take the first page or iterate
	// We'll trust the default page size is sufficient or loop
	// Using Pages() helper is safest

	call := c.service.Projects.Instances.List(parent)
	err := call.Pages(context.Background(), func(page *bigtableadmin.ListInstancesResponse) error {
		for _, i := range page.Instances {
			parts := strings.Split(i.Name, "/")
			shortName := parts[len(parts)-1]

			// Enum mapping for State/Type if needed, but strings are usually fine

			instances = append(instances, Instance{
				Name:        shortName,
				DisplayName: i.DisplayName,
				ProjectID:   projectID,
				State:       i.State,
				Type:        i.Type,
				CreateTime:  i.CreateTime,
				Edition:     i.Edition,
				Labels:      i.Labels,
			})
		}
		return nil
	})
	return instances, err
}

// CreateInstance creates a new Bigtable instance with a single cluster.
func (c *Client) CreateInstance(projectID, instanceID, displayName, clusterID, zone, storageType string, numNodes int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", projectID)
	req := &bigtableadmin.CreateInstanceRequest{
		Parent:     parent,
		InstanceId: instanceID,
		Instance: &bigtableadmin.Instance{
			DisplayName: displayName,
			Type:        "PRODUCTION",
		},
		Clusters: map[string]bigtableadmin.Cluster{
			clusterID: {
				Location:           fmt.Sprintf("projects/%s/locations/%s", projectID, zone),
				DefaultStorageType: storageType,
				ServeNodes:         int64(numNodes),
			},
		},
	}
	_, err := c.service.Projects.Instances.Create(parent, req).Do()
	return err
}

// UpdateClusterNodes resizes a Bigtable cluster's node count, matching
// `gcloud bigtable clusters update --num-nodes`. Autoscaling config changes
// and instance-level `upgrade` are out of scope.
func (c *Client) UpdateClusterNodes(projectID, instanceID, clusterID string, numNodes int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s/clusters/%s", projectID, instanceID, clusterID)
	cluster := &bigtableadmin.Cluster{
		ServeNodes: int64(numNodes),
	}
	_, err := c.service.Projects.Instances.Clusters.PartialUpdateCluster(name, cluster).UpdateMask("serve_nodes").Do()
	return err
}

// DeleteInstance deletes a Bigtable instance and all of its clusters and
// tables, matching `gcloud bigtable instances delete`.
func (c *Client) DeleteInstance(projectID, instanceID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	_, err := c.service.Projects.Instances.Delete(name).Do()
	return err
}

func (c *Client) ListClusters(projectID, instanceID string) ([]Cluster, error) {
	if demo.Enabled {
		return []Cluster{}, nil
	}
	var clusters []Cluster
	parent := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)

	call := c.service.Projects.Instances.Clusters.List(parent)
	err := call.Pages(context.Background(), func(page *bigtableadmin.ListClustersResponse) error {
		for _, cl := range page.Clusters {
			parts := strings.Split(cl.Name, "/")
			shortName := parts[len(parts)-1]

			zoneParts := strings.Split(cl.Location, "/")
			zone := ""
			if len(zoneParts) > 0 {
				zone = zoneParts[len(zoneParts)-1]
			}

			var autoMin, autoMax, autoCpuTarget int
			if cl.ClusterConfig != nil && cl.ClusterConfig.ClusterAutoscalingConfig != nil {
				ac := cl.ClusterConfig.ClusterAutoscalingConfig
				if ac.AutoscalingLimits != nil {
					autoMin = int(ac.AutoscalingLimits.MinServeNodes)
					autoMax = int(ac.AutoscalingLimits.MaxServeNodes)
				}
				if ac.AutoscalingTargets != nil {
					autoCpuTarget = int(ac.AutoscalingTargets.CpuUtilizationPercent)
				}
			}

			kmsKeyName := ""
			if cl.EncryptionConfig != nil {
				kmsKeyName = cl.EncryptionConfig.KmsKeyName
			}

			clusters = append(clusters, Cluster{
				Name:                 shortName,
				Zone:                 zone,
				ServeNodes:           int(cl.ServeNodes),
				State:                cl.State,
				StorageType:          cl.DefaultStorageType,
				AutoscalingMin:       autoMin,
				AutoscalingMax:       autoMax,
				AutoscalingCpuTarget: autoCpuTarget,
				KmsKeyName:           kmsKeyName,
			})
		}
		return nil
	})
	return clusters, err
}

// ListTables lists the data-plane tables inside a Bigtable instance,
// matching `gcloud bigtable instances tables list`. It requests SCHEMA_VIEW
// so the column-family names come back alongside each table's short name;
// table create/delete/restore/undelete are out of scope — this is read-only.
func (c *Client) ListTables(projectID, instanceID string) ([]TableInfo, error) {
	if demo.Enabled {
		var fixtures []TableInfo
		demo.MustLoad("bigtable_tables", &fixtures)
		return fixtures, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("bigtable client not initialized")
	}
	var tables []TableInfo
	parent := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)

	call := c.service.Projects.Instances.Tables.List(parent).View("SCHEMA_VIEW")
	err := call.Pages(context.Background(), func(page *bigtableadmin.ListTablesResponse) error {
		for _, t := range page.Tables {
			parts := strings.Split(t.Name, "/")
			shortName := parts[len(parts)-1]

			cfNames := make([]string, 0, len(t.ColumnFamilies))
			for cf := range t.ColumnFamilies {
				cfNames = append(cfNames, cf)
			}
			sort.Strings(cfNames)

			tables = append(tables, TableInfo{
				Name:           shortName,
				ColumnFamilies: cfNames,
			})
		}
		return nil
	})
	return tables, err
}
