package cloudsql

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	sqladmin "google.golang.org/api/sqladmin/v1beta4"
)

// Client wraps the Cloud SQL Admin API
type Client struct {
	service *sqladmin.Service
}

// NewClient creates a new Cloud SQL client
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	httpClient, err := core.NewHTTPClient(ctx, sqladmin.SqlserviceAdminScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	svc, err := sqladmin.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create sql client: %w", err)
	}

	return &Client{service: svc}, nil
}

// ListInstances fetches all Cloud SQL instances in the project
func (c *Client) ListInstances(projectID string) ([]Instance, error) {
	if demo.Enabled {
		return []Instance{}, nil
	}
	resp, err := c.service.Instances.List(projectID).Do()
	if err != nil {
		return nil, fmt.Errorf("cloud sql api error: %w", err)
	}

	var instances []Instance
	for _, item := range resp.Items {
		// Find Primary IP
		primaryIP := "N/A"
		for _, ip := range item.IpAddresses {
			if ip.Type == "PRIMARY" {
				primaryIP = ip.IpAddress
				break
			}
		}

		inst := Instance{
			Name:                item.Name,
			ProjectID:           item.Project,
			Region:              item.Region,
			DatabaseVersion:     item.DatabaseVersion,
			State:               InstanceState(item.State),
			PrimaryIP:           primaryIP,
			ConnectionName:      item.ConnectionName,
			Zone:                item.GceZone,
			MasterInstanceName:  item.MasterInstanceName,
			ReplicaNames:        item.ReplicaNames,
			ServiceAccountEmail: item.ServiceAccountEmailAddress,
			CreateTime:          item.CreateTime,
			InstanceType:        item.InstanceType,
		}

		// Detailed mapping
		if item.Settings != nil {
			inst.Tier = item.Settings.Tier
			inst.Activation = item.Settings.ActivationPolicy
			inst.DiskType = item.Settings.DataDiskType
			inst.AvailabilityType = item.Settings.AvailabilityType
			if item.Settings.DataDiskSizeGb > 0 {
				inst.StorageGB = item.Settings.DataDiskSizeGb
			}
			if item.Settings.BackupConfiguration != nil {
				inst.AutoBackup = item.Settings.BackupConfiguration.Enabled
				inst.PointInTimeRecovery = item.Settings.BackupConfiguration.PointInTimeRecoveryEnabled
			}
			if item.Settings.IpConfiguration != nil {
				inst.PublicIPEnabled = item.Settings.IpConfiguration.Ipv4Enabled
			}
			if item.Settings.MaintenanceWindow != nil {
				inst.MaintenanceDay = item.Settings.MaintenanceWindow.Day
				inst.MaintenanceHour = item.Settings.MaintenanceWindow.Hour
			}
		}

		instances = append(instances, inst)
	}

	return instances, nil
}

// StopInstance stops a Cloud SQL instance by setting activation policy to NEVER
func (c *Client) StopInstance(projectID, name string) error {
	rb := &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			ActivationPolicy: "NEVER",
		},
	}
	_, err := c.service.Instances.Patch(projectID, name, rb).Do()
	return err
}

// StartInstance starts a Cloud SQL instance by setting activation policy to ALWAYS
func (c *Client) StartInstance(projectID, name string) error {
	rb := &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			ActivationPolicy: "ALWAYS",
		},
	}
	_, err := c.service.Instances.Patch(projectID, name, rb).Do()
	return err
}

// DeleteInstance deletes a Cloud SQL instance, matching
// `gcloud sql instances delete`. This permanently destroys all databases on
// the instance with no undo — callers must require a double confirmation.
func (c *Client) DeleteInstance(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Instances.Delete(projectID, name).Do()
	return err
}

// CreateInstance creates a new Cloud SQL instance.
func (c *Client) CreateInstance(projectID, name, region, databaseVersion, tier string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}

	rb := &sqladmin.DatabaseInstance{
		Name:            name,
		Region:          region,
		DatabaseVersion: databaseVersion,
		Settings: &sqladmin.Settings{
			Tier: tier,
		},
	}

	_, err := c.service.Instances.Insert(projectID, rb).Do()
	return err
}

// UpdateInstanceTier patches the machine tier (e.g. db-custom-2-8192) of an
// existing Cloud SQL instance via a Settings-only Patch, matching
// `gcloud sql instances patch --tier`. Storage size, flags, and other
// settings fields are left untouched; full `sql instances patch` surface
// coverage is intentionally out of scope.
func (c *Client) UpdateInstanceTier(projectID, name, tier string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	rb := &sqladmin.DatabaseInstance{
		Settings: &sqladmin.Settings{
			Tier: tier,
		},
	}
	_, err := c.service.Instances.Patch(projectID, name, rb).Do()
	return err
}

// RestartInstance restarts a Cloud SQL instance.
func (c *Client) RestartInstance(projectID, name string) error {
	_, err := c.service.Instances.Restart(projectID, name).Do()
	return err
}

// writeKeywordRe matches SQL keywords that mutate data or schema. It is
// checked against the whole statement body (not just its prefix) so that a
// disallowed write hidden inside a CTE -- e.g.
// `WITH x AS (DELETE FROM t RETURNING *) SELECT * FROM x` -- is still caught.
var writeKeywordRe = regexp.MustCompile(`(?i)\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|replace|merge|call|lock|unlock|set|begin|commit|rollback|copy|vacuum|analyze|reindex|comment|into\s+outfile)\b`)

// readOnlyPrefixes lists the statement forms this feature allows. Anything
// else is rejected before it ever reaches the API.
var readOnlyPrefixes = []string{"select", "with", "show", "explain", "describe", "desc "}

// ValidateReadOnlySQL enforces that a statement is a single, read-only query
// before it is sent to the Cloud SQL Admin API's ExecuteSql RPC. This is a
// client-side safety net for the "execute-sql" data-plane feature -- it is
// not a substitute for proper database-level permissions, but it keeps this
// read-only feature from ever issuing an INSERT/UPDATE/DELETE/DDL statement.
func ValidateReadOnlySQL(stmt string) error {
	trimmed := strings.TrimSpace(stmt)
	if trimmed == "" {
		return fmt.Errorf("SQL statement is required")
	}
	body := strings.TrimRight(trimmed, "; \t\n")
	if strings.Contains(body, ";") {
		return fmt.Errorf("only a single statement is allowed (no ';'-separated statements)")
	}
	lower := strings.ToLower(body)
	ok := false
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(lower, p) {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("only SELECT/WITH/SHOW/EXPLAIN/DESCRIBE statements are allowed")
	}
	if writeKeywordRe.MatchString(lower) {
		return fmt.Errorf("statement contains a disallowed write keyword")
	}
	return nil
}

// ExecuteQuery runs a single read-only SQL statement against a Cloud SQL
// instance via the Admin API's `instances.executeSql` RPC and returns a
// simplified result set. Authentication to the database uses the caller's
// IAM identity (AutoIamAuthn) -- the same credentials tgcp already uses to
// call the Admin API -- so no database password is ever collected or
// stored. Only read-only statements are accepted; see ValidateReadOnlySQL.
func (c *Client) ExecuteQuery(projectID, instanceName, database, sqlStatement string) (*QueryResult, error) {
	if err := ValidateReadOnlySQL(sqlStatement); err != nil {
		return nil, err
	}

	if demo.Enabled {
		return &QueryResult{
			Columns: []QueryColumn{{Name: "id", Type: "int8"}, {Name: "name", Type: "varchar"}},
			Rows: []QueryRow{
				{Values: []string{"1", "demo-row-one"}},
				{Values: []string{"2", "demo-row-two"}},
			},
			Message: "Demo mode: showing canned results, no live query was executed.",
		}, nil
	}

	if c.service == nil {
		return nil, fmt.Errorf("sql client not initialized")
	}

	payload := &sqladmin.ExecuteSqlPayload{
		Database:          database,
		SqlStatement:      sqlStatement,
		AutoIamAuthn:      true,
		RowLimit:          200,
		PartialResultMode: "ALLOW_PARTIAL_RESULT",
		Application:       "tgcp",
	}

	resp, err := c.service.Instances.ExecuteSql(projectID, instanceName, payload).Do()
	if err != nil {
		return nil, fmt.Errorf("execute sql: %w", err)
	}
	if resp.Status != nil && resp.Status.Message != "" {
		return nil, fmt.Errorf("execute sql: %s", resp.Status.Message)
	}
	if len(resp.Results) == 0 {
		return &QueryResult{Message: "Query executed successfully, no results returned."}, nil
	}

	// Only the first statement's result is surfaced -- ValidateReadOnlySQL
	// already rejects multi-statement input, so there should only ever be one.
	first := resp.Results[0]
	if first.Status != nil && first.Status.Message != "" {
		return nil, fmt.Errorf("execute sql: %s", first.Status.Message)
	}

	qr := &QueryResult{
		Truncated: first.PartialResult,
		Message:   first.Message,
	}
	for _, col := range first.Columns {
		qr.Columns = append(qr.Columns, QueryColumn{Name: col.Name, Type: col.Type})
	}
	for _, row := range first.Rows {
		vals := make([]string, len(row.Values))
		for i, v := range row.Values {
			if v.NullValue {
				vals[i] = "NULL"
			} else {
				vals[i] = v.Value
			}
		}
		qr.Rows = append(qr.Rows, QueryRow{Values: vals})
	}
	if resp.Metadata != nil {
		qr.ExecutionTime = resp.Metadata.SqlStatementExecutionTime
	}
	return qr, nil
}
