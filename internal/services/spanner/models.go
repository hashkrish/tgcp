package spanner

type Instance struct {
	Name                      string // Short ID
	DisplayName               string
	ProjectID                 string
	Config                    string // regional-us-central1
	State                     string // READY
	NodeCount                 int
	ProcessingUnits           int
	Labels                    map[string]string
	Edition                   string
	DefaultBackupScheduleType string
	CreateTime                string
	UpdateTime                string
}

// QueryRow holds the rendered (already stringified) cell values for one row
// of a QueryResult, in the same order as QueryResult.Columns.
type QueryRow struct {
	Values []string
}

// QueryResult is a simplified, read-only view of a Spanner SQL query
// result, used only by the "Execute SQL" data-plane feature. Every
// QueryResult is guaranteed to come from a statement that passed
// validateReadOnlySQL -- there is no code path in this package that builds
// one from a DML/DDL statement.
type QueryResult struct {
	Columns   []string
	Rows      []QueryRow
	Truncated bool // true if the result was cut short by queryRowLimit
}
