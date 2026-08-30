package core

import (
	"testing"
	"time"
)

func TestRecordAndLoadJobs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	SetJobRetention(500, 30*24*time.Hour)

	RecordJob(Job{ProjectID: "p1", Service: "gce", Resource: "instance", Name: "vm-1", Action: "stop", Status: JobSuccess})
	RecordJob(Job{ProjectID: "p1", Service: "pubsub", Resource: "subscription", Name: "sub-1", Action: "delete", Status: JobFailed, Error: "permission denied"})

	jobs := LoadJobs()
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	// Newest first.
	if jobs[0].Service != "pubsub" || jobs[1].Service != "gce" {
		t.Fatalf("expected newest-first order, got %+v", jobs)
	}
	if jobs[0].Status != JobFailed || jobs[0].Error != "permission denied" {
		t.Fatalf("unexpected failed job: %+v", jobs[0])
	}
}

func TestRecordJobRetentionByCount(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	SetJobRetention(2, 30*24*time.Hour)

	for range 5 {
		RecordJob(Job{ProjectID: "p1", Service: "gce", Resource: "instance", Name: "vm", Action: "stop", Status: JobSuccess})
	}

	jobs := LoadJobs()
	if len(jobs) != 2 {
		t.Fatalf("expected retention to cap at 2 jobs, got %d", len(jobs))
	}
}

func TestRecordJobRetentionByAge(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	SetJobRetention(500, time.Hour)

	old := Job{ProjectID: "p1", Service: "gce", Resource: "instance", Name: "old-vm", Action: "stop", Status: JobSuccess, OccurredAt: time.Now().Add(-48 * time.Hour)}
	saveJobs([]Job{old})

	RecordJob(Job{ProjectID: "p1", Service: "gce", Resource: "instance", Name: "new-vm", Action: "stop", Status: JobSuccess})

	jobs := LoadJobs()
	if len(jobs) != 1 || jobs[0].Name != "new-vm" {
		t.Fatalf("expected old job pruned by age, got %+v", jobs)
	}
}
