package cloudsql

// InstanceState represents the status of a Cloud SQL instance
type InstanceState string

const (
	StateRunnable    InstanceState = "RUNNABLE"
	StateSuspended   InstanceState = "SUSPENDED"
	StatePending     InstanceState = "PENDING_CREATE"
	StateMaintenance InstanceState = "MAINTENANCE"
	StateFailed      InstanceState = "FAILED"
	StateUnknown     InstanceState = "UNKNOWN"
)

// Instance represents a simplified Cloud SQL instance
type Instance struct {
	Name            string
	ProjectID       string
	Region          string
	DatabaseVersion string
	State           InstanceState
	Tier            string
	PrimaryIP       string
	ConnectionName  string

	// Details
	StorageGB  int64
	AutoBackup bool
	Activation string // ALWAYS or NEVER

	// Additional details (already returned by Instances.List, just not surfaced before)
	Zone               string // GCE zone the instance runs in
	DiskType           string // PD_SSD or PD_HDD
	AvailabilityType   string // ZONAL or REGIONAL (High Availability)
	PublicIPEnabled    bool
	MaintenanceDay     int64 // 1 (Monday) - 7 (Sunday)
	MaintenanceHour    int64 // 0-23 UTC
	MasterInstanceName string
	ReplicaNames       []string

	ServiceAccountEmail string
	CreateTime          string
	InstanceType        string // CLOUD_SQL_INSTANCE, READ_REPLICA_INSTANCE, ...
	PointInTimeRecovery bool
}

// QueryColumn describes one column of a QueryResult.
type QueryColumn struct {
	Name string
	Type string
}

// QueryRow holds the rendered (already stringified) cell values for one row
// of a QueryResult, in the same order as QueryResult.Columns.
type QueryRow struct {
	Values []string
}

// QueryResult is a simplified, read-only view of a Cloud SQL Admin API
// `instances.executeSql` response, used only by the "Execute SQL" data-plane
// feature. Every QueryResult is guaranteed to come from a statement that
// passed ValidateReadOnlySQL -- there is no code path in this package that
// builds one from an INSERT/UPDATE/DELETE/DDL statement.
type QueryResult struct {
	Columns       []QueryColumn
	Rows          []QueryRow
	Truncated     bool   // true if the result was cut short (row/size limit)
	Message       string // informational message from the API, if any
	ExecutionTime string
}
