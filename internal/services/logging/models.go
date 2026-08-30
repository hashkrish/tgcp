package logging

import "time"

// LogEntry represents a unified log entry for display
type LogEntry struct {
	Timestamp time.Time
	Severity  string
	Payload   string

	ResourceType string // gce_instance, cloud_run_revision
	ResourceName string // vm-name, service-name
	Location     string // us-central1
	ProjectID    string

	LogName string
	Labels  map[string]string

	InsertID    string
	FullPayload string
}

// Sink represents a log export destination (read-only view).
type Sink struct {
	Name        string // Short sink ID
	Destination string // e.g. storage.googleapis.com/BUCKET, bigquery.googleapis.com/...
	Filter      string
	Disabled    bool
}

// LogMetric represents a logs-based metric (read-only view).
type LogMetric struct {
	Name        string // Short metric ID
	Description string
	Filter      string
}

// LogBucket represents a log bucket (read-only view).
type LogBucket struct {
	Name          string // Short bucket ID
	FullName      string // projects/{project}/locations/{location}/buckets/{id}
	RetentionDays int64
	Locked        bool
}

// LogView represents a view over a log bucket (read-only view).
type LogView struct {
	Name     string // Short view ID
	FullName string // projects/{project}/locations/{location}/buckets/{bucket}/views/{id}
	Filter   string
}

// IAMBinding pairs a role with its granted members, matching the shape
// used across other services' AddIAMBinding read-before-grant flows.
type IAMBinding struct {
	Role    string
	Members []string
}
