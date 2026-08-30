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

// IAMBinding is a single role -> members pair from a queue's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}

// Task represents a single Cloud Tasks task (read-only view). Only the
// HTTP-target request shape is covered -- App Engine-target tasks are
// deliberately out of scope for the Create flow, matching this codebase's
// general minimal-viable-scope pattern.
type Task struct {
	Name         string // Short task ID
	URL          string // HTTP target URL, if this is an HTTP task
	HTTPMethod   string
	ScheduleTime string
	CreateTime   string
	DispatchCnt  int32
	ResponseCnt  int32
}
