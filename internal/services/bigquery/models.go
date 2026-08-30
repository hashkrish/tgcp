package bigquery

import "time"

type Dataset struct {
	ID           string
	ProjectID    string
	Location     string
	Description  string
	Labels       map[string]string
	CreationTime time.Time
}

type Table struct {
	ID             string
	DatasetID      string
	Type           string // "TABLE", "VIEW", "MATERIALIZED_VIEW"
	NumRows        uint64
	TotalBytes     int64
	LastMod        time.Time
	Description    string
	CreationTime   time.Time
	ExpirationTime time.Time
	Partitioning   string
}

type SchemaField struct {
	Name        string
	Type        string
	Mode        string // NULLABLE, REQUIRED, REPEATED
	Description string
}

// QueryResult holds the output of an ad-hoc SQL query (SELECT, INSERT,
// CREATE TABLE AS SELECT, etc.), capped at queryResultRowLimit rows for
// display -- this app has no paginated data-plane browser, so a large
// result set is truncated rather than fully buffered.
type QueryResult struct {
	Columns   []string
	Rows      [][]string
	RowCount  int // total rows returned by the query, before truncation
	Truncated bool
}

// queryResultRowLimit caps how many result rows RunQuery renders.
const queryResultRowLimit = 50
