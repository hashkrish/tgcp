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
