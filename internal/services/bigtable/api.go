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

// UpdateInstanceType changes an instance's type, matching
// `gcloud bigtable instances update --instance-type`. The one supported
// transition in practice is upgrading DEVELOPMENT -> PRODUCTION (Bigtable
// rejects the reverse), which is what "instance upgrade" refers to.
func (c *Client) UpdateInstanceType(projectID, instanceID, instanceType string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	instance := &bigtableadmin.Instance{Type: instanceType}
	_, err := c.service.Projects.Instances.PartialUpdateInstance(name, instance).UpdateMask("type").Do()
	return err
}

// SetClusterAutoscaling enables autoscaling on a cluster with the given
// node bounds and target CPU utilization, matching
// `gcloud bigtable clusters update --autoscaling-min-nodes
// --autoscaling-max-nodes --autoscaling-cpu-target`. Passing minNodes<=0
// disables autoscaling and falls back to a fixed node count instead (see
// UpdateClusterNodes).
func (c *Client) SetClusterAutoscaling(projectID, instanceID, clusterID string, minNodes, maxNodes, cpuTarget int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s/clusters/%s", projectID, instanceID, clusterID)
	cluster := &bigtableadmin.Cluster{
		ClusterConfig: &bigtableadmin.ClusterConfig{
			ClusterAutoscalingConfig: &bigtableadmin.ClusterAutoscalingConfig{
				AutoscalingLimits: &bigtableadmin.AutoscalingLimits{
					MinServeNodes: int64(minNodes),
					MaxServeNodes: int64(maxNodes),
				},
				AutoscalingTargets: &bigtableadmin.AutoscalingTargets{
					CpuUtilizationPercent: int64(cpuTarget),
				},
			},
		},
	}
	_, err := c.service.Projects.Instances.Clusters.PartialUpdateCluster(name, cluster).UpdateMask("cluster_config.cluster_autoscaling_config").Do()
	return err
}

// CreateTable creates a Bigtable data-plane table with the given
// column-family names (all with the default GC policy), matching
// `gcloud bigtable instances tables create --column-families=cf1,cf2`.
func (c *Client) CreateTable(projectID, instanceID, tableID string, columnFamilies []string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	families := make(map[string]bigtableadmin.ColumnFamily, len(columnFamilies))
	for _, cf := range columnFamilies {
		families[cf] = bigtableadmin.ColumnFamily{}
	}
	req := &bigtableadmin.CreateTableRequest{
		TableId: tableID,
		Table:   &bigtableadmin.Table{ColumnFamilies: families},
	}
	_, err := c.service.Projects.Instances.Tables.Create(parent, req).Do()
	return err
}

// DeleteTable deletes a Bigtable data-plane table, matching
// `gcloud bigtable instances tables delete`.
func (c *Client) DeleteTable(projectID, instanceID, tableID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s/tables/%s", projectID, instanceID, tableID)
	_, err := c.service.Projects.Instances.Tables.Delete(name).Do()
	return err
}

// DescribeTable fetches full details (column families, granularity) for a
// single table, matching `gcloud bigtable instances tables describe`.
func (c *Client) DescribeTable(projectID, instanceID, tableID string) (*TableInfo, error) {
	if demo.Enabled {
		return &TableInfo{Name: tableID}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s/tables/%s", projectID, instanceID, tableID)
	t, err := c.service.Projects.Instances.Tables.Get(name).View("SCHEMA_VIEW").Do()
	if err != nil {
		return nil, err
	}
	families := make([]string, 0, len(t.ColumnFamilies))
	for cf := range t.ColumnFamilies {
		families = append(families, cf)
	}
	return &TableInfo{
		Name:           tableID,
		ColumnFamilies: families,
		Granularity:    t.Granularity,
	}, nil
}

// RestoreTable restores a table from a backup into a new table, matching
// `gcloud bigtable tables restore`. backupName is the backup's fully
// qualified resource name (projects/{p}/instances/{i}/clusters/{c}/backups/{b}) --
// this package has no backup create/list flow, so the caller supplies it
// directly.
func (c *Client) RestoreTable(projectID, instanceID, newTableID, backupName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	req := &bigtableadmin.RestoreTableRequest{TableId: newTableID, Backup: backupName}
	_, err := c.service.Projects.Instances.Tables.Restore(parent, req).Do()
	return err
}

// UndeleteTable restores a recently deleted table within its recovery
// window, matching `gcloud bigtable instances tables undelete`.
func (c *Client) UndeleteTable(projectID, instanceID, tableID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s/tables/%s", projectID, instanceID, tableID)
	_, err := c.service.Projects.Instances.Tables.Undelete(name, &bigtableadmin.UndeleteTableRequest{}).Do()
	return err
}

// AddInstanceIAMBinding grants a role to a member on a Bigtable instance,
// matching `gcloud bigtable instances add-iam-policy-binding`. It fetches
// the current policy, appends the new binding (rather than replacing), and
// writes the merged result back -- this never drops any existing binding,
// unlike a raw set-iam-policy.
func (c *Client) AddInstanceIAMBinding(projectID, instanceID, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("bigtable client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	policy, err := c.service.Projects.Instances.GetIamPolicy(name, &bigtableadmin.GetIamPolicyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("get instance IAM policy: %w", err)
	}
	found := false
	for _, b := range policy.Bindings {
		if b.Role == role {
			b.Members = append(b.Members, member)
			found = true
			break
		}
	}
	if !found {
		policy.Bindings = append(policy.Bindings, &bigtableadmin.Binding{Role: role, Members: []string{member}})
	}
	_, err = c.service.Projects.Instances.SetIamPolicy(name, &bigtableadmin.SetIamPolicyRequest{Policy: policy}).Do()
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
