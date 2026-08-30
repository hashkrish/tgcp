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

// FailoverInstance triggers a failover from a High Availability primary to
// its standby, matching `gcloud sql instances failover`. Only applicable to
// instances with AvailabilityType REGIONAL; the API rejects the call
// otherwise, and that error is surfaced to the caller as-is.
func (c *Client) FailoverInstance(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Instances.Failover(projectID, name, &sqladmin.InstancesFailoverRequest{}).Do()
	return err
}

// PromoteReplica promotes a read replica to a standalone primary instance,
// matching `gcloud sql instances promote-replica`. This is irreversible:
// once promoted, the instance stops replicating from its former primary.
func (c *Client) PromoteReplica(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Instances.PromoteReplica(projectID, name).Do()
	return err
}

// CloneInstance clones a Cloud SQL instance into a new instance named
// destName, matching `gcloud sql instances clone`. If pointInTime is
// non-empty (RFC 3339), the clone is taken as of that point in time
// instead of the current state -- this is also how point-in-time recovery
// works in the Cloud SQL Admin API: there is no separate PITR-restore RPC,
// just a clone with CloneContext.PointInTime set.
func (c *Client) CloneInstance(projectID, name, destName, pointInTime string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	req := &sqladmin.InstancesCloneRequest{
		CloneContext: &sqladmin.CloneContext{
			DestinationInstanceName: destName,
			PointInTime:             pointInTime,
		},
	}
	_, err := c.service.Instances.Clone(projectID, name, req).Do()
	return err
}

// SwitchoverInstance triggers a planned switchover between a Cloud SQL
// primary and its cross-region replica, matching `gcloud sql instances
// switchover`. Only applicable to instances configured for replication;
// the API rejects the call otherwise.
func (c *Client) SwitchoverInstance(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Instances.Switchover(projectID, name).Do()
	return err
}

// ListDatabases lists the databases on a Cloud SQL instance, matching
// `gcloud sql databases list`.
func (c *Client) ListDatabases(projectID, instance string) ([]Database, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("sql client not initialized")
	}
	resp, err := c.service.Databases.List(projectID, instance).Do()
	if err != nil {
		return nil, err
	}
	dbs := make([]Database, 0, len(resp.Items))
	for _, d := range resp.Items {
		dbs = append(dbs, Database{Name: d.Name, Charset: d.Charset, Collation: d.Collation})
	}
	return dbs, nil
}

// CreateDatabase creates a new database on a Cloud SQL instance, matching
// `gcloud sql databases create`.
func (c *Client) CreateDatabase(projectID, instance, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Databases.Insert(projectID, instance, &sqladmin.Database{Name: name}).Do()
	return err
}

// DeleteDatabase deletes a database from a Cloud SQL instance, matching
// `gcloud sql databases delete`. This permanently destroys all data in the
// database with no undo.
func (c *Client) DeleteDatabase(projectID, instance, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Databases.Delete(projectID, instance, name).Do()
	return err
}

// ListUsers lists the database users on a Cloud SQL instance, matching
// `gcloud sql users list`.
func (c *Client) ListUsers(projectID, instance string) ([]DBUser, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("sql client not initialized")
	}
	resp, err := c.service.Users.List(projectID, instance).Do()
	if err != nil {
		return nil, err
	}
	users := make([]DBUser, 0, len(resp.Items))
	for _, u := range resp.Items {
		users = append(users, DBUser{Name: u.Name, Host: u.Host, Type: u.Type})
	}
	return users, nil
}

// CreateUser creates a new database user on a Cloud SQL instance, matching
// `gcloud sql users create`.
func (c *Client) CreateUser(projectID, instance, name, password string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	_, err := c.service.Users.Insert(projectID, instance, &sqladmin.User{Name: name, Password: password}).Do()
	return err
}

// DeleteUser deletes a database user from a Cloud SQL instance, matching
// `gcloud sql users delete`. host identifies which host-scoped user to
// delete (MySQL-style host wildcards); pass "" for engines without
// host-scoped users (Postgres, SQL Server).
func (c *Client) DeleteUser(projectID, instance, name, host string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("sql client not initialized")
	}
	call := c.service.Users.Delete(projectID, instance).Name(name)
	if host != "" {
		call = call.Host(host)
	}
	_, err := call.Do()
	return err
}

// ListBackups lists the automated/on-demand backup runs for a Cloud SQL
// instance, matching `gcloud sql backups list`. Read-only -- there is no
// Create/Delete flow for backups in this minimal viable coverage, since a
// backup run is normally driven by the instance's automated backup
// schedule rather than ad hoc from this TUI.
func (c *Client) ListBackups(projectID, instance string) ([]Backup, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("sql client not initialized")
	}
	resp, err := c.service.BackupRuns.List(projectID, instance).Do()
	if err != nil {
		return nil, err
	}
	backups := make([]Backup, 0, len(resp.Items))
	for _, b := range resp.Items {
		backups = append(backups, Backup{
			ID:        b.Id,
			Status:    b.Status,
			Type:      b.Type,
			StartTime: b.StartTime,
			EndTime:   b.EndTime,
		})
	}
	return backups, nil
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
