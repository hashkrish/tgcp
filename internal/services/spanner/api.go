package spanner

import (
	"context"
	"fmt"
	"strings"

	spannerdata "cloud.google.com/go/spanner"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/iterator"
	"google.golang.org/api/spanner/v1"
)

// queryRowLimit caps how many rows ExecuteQuery renders, mirroring the
// row-limiting behavior of Cloud SQL's "Execute SQL" data-plane feature.
const queryRowLimit = 200

// validateReadOnlySQL rejects anything but a single SELECT/WITH query,
// matching the read-only guard cloudsql.ValidateReadOnlySQL provides for
// its own Execute SQL feature (not shared package-to-package since the
// dialect differs slightly -- Spanner has no SHOW/EXPLAIN/DESCRIBE).
func validateReadOnlySQL(stmt string) error {
	trimmed := strings.TrimSpace(stmt)
	if trimmed == "" {
		return fmt.Errorf("SQL statement is required")
	}
	body := strings.TrimRight(trimmed, "; \t\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("only a single statement is allowed (no ';'-separated statements)")
	}
	lower := strings.ToLower(body)
	if !strings.HasPrefix(lower, "select") && !strings.HasPrefix(lower, "with") {
		return fmt.Errorf("only SELECT/WITH statements are allowed")
	}
	return nil
}

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

// AddInstanceIAMBinding grants a role to a member on a Spanner instance,
// matching `gcloud spanner instances add-iam-policy-binding`. It fetches
// the current policy, appends the new binding (rather than replacing), and
// writes the merged result back -- this never drops any existing binding,
// unlike a raw set-iam-policy.
func (c *Client) AddInstanceIAMBinding(projectID, instanceID, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("spanner client not initialized")
	}
	name := fmt.Sprintf("projects/%s/instances/%s", projectID, instanceID)
	policy, err := c.service.Projects.Instances.GetIamPolicy(name, &spanner.GetIamPolicyRequest{}).Do()
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
		policy.Bindings = append(policy.Bindings, &spanner.Binding{Role: role, Members: []string{member}})
	}
	_, err = c.service.Projects.Instances.SetIamPolicy(name, &spanner.SetIamPolicyRequest{Policy: policy}).Do()
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

// ExecuteQuery runs a read-only SQL statement against a Spanner database,
// matching `gcloud spanner databases execute-sql`. Unlike the rest of this
// package (which uses the Instance Admin REST API), this hits Spanner's
// data-plane client library directly -- there is no admin-API equivalent
// of Cloud SQL's `instances.executeSql` REST trick for Spanner. A fresh
// data client is created per call and closed afterward, since this is a
// rarely-invoked, minimal-viable feature rather than a hot path worth
// pooling a long-lived client for.
func (c *Client) ExecuteQuery(projectID, instanceID, databaseID, sqlStatement string) (*QueryResult, error) {
	if err := validateReadOnlySQL(sqlStatement); err != nil {
		return nil, err
	}
	if demo.Enabled {
		return &QueryResult{
			Columns: []string{"id", "name"},
			Rows: []QueryRow{
				{Values: []string{"1", "demo-row-one"}},
				{Values: []string{"2", "demo-row-two"}},
			},
		}, nil
	}

	ctx := context.Background()
	dbPath := fmt.Sprintf("projects/%s/instances/%s/databases/%s", projectID, instanceID, databaseID)
	dataClient, err := spannerdata.NewClient(ctx, dbPath)
	if err != nil {
		return nil, fmt.Errorf("spanner data client: %w", err)
	}
	defer dataClient.Close()

	txn := dataClient.Single()
	defer txn.Close()

	iter := txn.Query(ctx, spannerdata.Statement{SQL: sqlStatement})
	defer iter.Stop()

	result := &QueryResult{}
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("execute sql: %w", err)
		}
		if result.Columns == nil {
			result.Columns = row.ColumnNames()
		}
		if len(result.Rows) >= queryRowLimit {
			result.Truncated = true
			break
		}
		values := make([]string, row.Size())
		for i := 0; i < row.Size(); i++ {
			values[i] = fmt.Sprintf("%v", row.ColumnValue(i).AsInterface())
		}
		result.Rows = append(result.Rows, QueryRow{Values: values})
	}
	return result, nil
}
