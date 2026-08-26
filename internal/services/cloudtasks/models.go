package cloudtasks

// Queue represents a Cloud Tasks queue (read-only view).
//
// Note: the Cloud Tasks v2 Queue message does not include task counts in
// ListQueues responses (no queue-level "stats" field is returned without a
// separate per-queue call), so we deliberately don't show a task count here
// — adding a GetQueue/stats call per queue would turn a single list call
// into an N+1 pattern that gets slow once a project has many queues.
type Queue struct {
	Name             string // Short queue ID
	Location         string // Region, e.g. us-central1
	State            string // RUNNING, PAUSED, DISABLED
	MaxDispatchRate  float64
	MaxConcurrent    int32
	MaxAttempts      int32
	MaxRetryDuration string
}
