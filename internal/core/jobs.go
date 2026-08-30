package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Job records the outcome of a single non-read (mutating) operation
// performed against GCP through the app -- e.g. stopping an instance,
// deleting a subscription, granting an IAM binding. Read/list operations are
// never recorded, successful or not.
//
// There is no "pending"/"running" status: every mutating call in this
// codebase is synchronous fire-and-forget (no long-running-operation
// polling exists anywhere), so a Job's outcome is always known by the time
// it's recorded.
type Job struct {
	ID         string    `json:"id"`
	ProjectID  string    `json:"project_id"`
	Service    string    `json:"service"`  // short name, e.g. "gce"
	Resource   string    `json:"resource"` // e.g. "instance", "subscription"
	Name       string    `json:"name"`     // resource name/ID
	Action     string    `json:"action"`   // e.g. "stop", "delete", "create"
	Status     string    `json:"status"`   // "success" | "failed"
	Error      string    `json:"error,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

const (
	JobSuccess = "success"
	JobFailed  = "failed"
)

// jobRetention is package-level configuration (set once at startup via
// SetJobRetention) rather than threaded through every service constructor --
// same idiom as internal/ui/components/termsize.go's globalWidth/
// SetGlobalSize, chosen so ~30 services can call RecordJob without changing
// their NewService(cache *core.Cache) signature or the services.Service
// interface.
var (
	jobRetentionMu sync.Mutex
	jobMaxCount    = 500
	jobMaxAge      = 30 * 24 * time.Hour
)

// SetJobRetention configures how many jobs (and how far back) are kept.
// maxCount <= 0 means unbounded by count; maxAge <= 0 means unbounded by
// age. Call once at startup, after config.LoadConfig().
func SetJobRetention(maxCount int, maxAge time.Duration) {
	jobRetentionMu.Lock()
	defer jobRetentionMu.Unlock()
	jobMaxCount = maxCount
	jobMaxAge = maxAge
}

func getJobRetention() (int, time.Duration) {
	jobRetentionMu.Lock()
	defer jobRetentionMu.Unlock()
	return jobMaxCount, jobMaxAge
}

// jobsStatePath returns the path to the persisted job history file,
// ~/.tgcp/jobs.json (same ~/.tgcp directory used for debug.log and
// recent.json).
func jobsStatePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".tgcp", "jobs.json"), nil
}

type jobsState struct {
	Jobs []Job `json:"jobs"`
}

// LoadJobs reads the persisted job history, newest first. Any error
// (missing file, unreadable home dir, corrupt JSON) is treated as "no
// history yet" rather than surfaced -- this is local app state, not
// load-bearing for tgcp's correctness.
func LoadJobs() []Job {
	path, err := jobsStatePath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var state jobsState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil
	}
	jobs := state.Jobs
	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].OccurredAt.After(jobs[j].OccurredAt)
	})
	return jobs
}

// saveJobs persists the job history. Best-effort: write failures (read-only
// home, disk full) are silently ignored since losing this history doesn't
// affect app correctness.
func saveJobs(jobs []Job) {
	path, err := jobsStatePath()
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return
	}
	data, err := json.Marshal(jobsState{Jobs: jobs})
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0644)
}

// RecordJob appends a completed job to the persisted history and enforces
// retention (age cutoff first, then a count cap -- both limits always
// apply). Best-effort: never blocks the caller on I/O failure.
func RecordJob(j Job) {
	if j.OccurredAt.IsZero() {
		j.OccurredAt = time.Now()
	}
	if j.ID == "" {
		j.ID = j.OccurredAt.Format("20060102T150405.000000000")
	}

	jobs := LoadJobs()
	jobs = append(jobs, j)

	maxCount, maxAge := getJobRetention()

	if maxAge > 0 {
		cutoff := time.Now().Add(-maxAge)
		kept := jobs[:0]
		for _, job := range jobs {
			if job.OccurredAt.After(cutoff) {
				kept = append(kept, job)
			}
		}
		jobs = kept
	}

	sort.Slice(jobs, func(i, j int) bool {
		return jobs[i].OccurredAt.After(jobs[j].OccurredAt)
	})

	if maxCount > 0 && len(jobs) > maxCount {
		jobs = jobs[:maxCount]
	}

	saveJobs(jobs)
}
