package scheduler

// Job represents a Cloud Scheduler cron job (read-only view).
type Job struct {
	Name          string // Short job ID
	Location      string // Region, e.g. us-central1
	Schedule      string // Cron expression or App Engine-style schedule
	TimeZone      string
	State         string // ENABLED, PAUSED, DISABLED, UPDATE_FAILED
	TargetType    string // HTTP, Pub/Sub, App Engine
	TargetSummary string // URI or Pub/Sub topic name
	ScheduleTime  string // Next scheduled run time (RFC3339), empty if unknown
	LastAttempt   string // Last attempt time (RFC3339), empty if never run
	RetryCount    int32
	MaxRetryDur   string
}
