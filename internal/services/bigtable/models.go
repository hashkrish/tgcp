package bigtable

type Instance struct {
	Name        string // Short ID
	DisplayName string
	ProjectID   string
	State       string // READY
	Type        string // PRODUCTION / DEVELOPMENT
	CreateTime  string
	Edition     string // STANDARD / ENTERPRISE
	Labels      map[string]string
}

type Cluster struct {
	Name                 string
	Zone                 string
	ServeNodes           int
	State                string
	StorageType          string // SSD / HDD
	AutoscalingMin       int    // 0 if autoscaling is disabled
	AutoscalingMax       int
	AutoscalingCpuTarget int // target CPU utilization percent, 0 if autoscaling is disabled
	KmsKeyName           string
}

// TableInfo is a read-only summary of a Bigtable data-plane table: its short
// name and the column families configured on it. Create/delete/restore for
// tables are out of scope — this backs a read-only listing drill-down only.
type TableInfo struct {
	Name           string
	ColumnFamilies []string
	// Granularity is the table's timestamp granularity (e.g. "MILLIS"),
	// populated only by DescribeTable.
	Granularity string
}
