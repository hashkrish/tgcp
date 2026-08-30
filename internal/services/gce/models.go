package gce

import "time"

// InstanceState represents the status of a VM
type InstanceState string

const (
	StateRunning      InstanceState = "RUNNING"
	StateStopped      InstanceState = "STOPPED"
	StateTerminated   InstanceState = "TERMINATED"
	StateProvisioning InstanceState = "PROVISIONING"
	StateStaging      InstanceState = "STAGING"
	StateStopping     InstanceState = "STOPPING"
	StateSuspending   InstanceState = "SUSPENDING"
	StateRepairing    InstanceState = "REPAIRING"
	StateOther        InstanceState = "OTHER"
)

// Disk represents a GCE disk
type Disk struct {
	Name   string
	SizeGB int64
	Type   string // e.g. "pd-standard", "pd-ssd"
}

// Instance represents a simplified GCE VM
type Instance struct {
	ID           string
	Name         string
	Zone         string
	State        InstanceState
	MachineType  string
	InternalIP   string
	ExternalIP   string
	CreationTime time.Time
	Tags         []string
	Disks        []Disk
	OSImage      string
}

// InstanceGroup represents a Managed Instance Group (MIG). Fields are
// limited to what the ListInstanceGroups AggregatedList call itself
// returns — no per-group follow-up API calls are made, so there's no
// separate "current size" (that requires a per-group instanceGroups.get
// call); Status reports whether the group is Stable or still converging
// toward TargetSize instead.
type InstanceGroup struct {
	Name             string
	Location         string // zone or region name
	Regional         bool
	TargetSize       int64
	InstanceTemplate string
	AutoscalingOn    bool
	Status           string // "Stable" or "Updating" (derived from Status.IsStable)
}

// IAMBinding is a single role -> members pair from a VM instance's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}
