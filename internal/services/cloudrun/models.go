package cloudrun

import "time"

type ServiceStatus string

const (
	StatusReady   ServiceStatus = "Ready"
	StatusFailed  ServiceStatus = "Failed"
	StatusUnknown ServiceStatus = "Unknown"
)

type RunService struct {
	Name         string
	Region       string
	URL          string
	Status       ServiceStatus
	LastModified time.Time
	// Image is the container image of the first container in the service's
	// revision template. Populated by ListServices; used to seed the Update
	// form's default value. Empty in demo mode (fixtures don't carry it).
	Image string
	// LatestReadyRevision is the name of the latest revision that passed
	// its Ready condition. May differ from a Revisions entry with
	// LatestRevision=true if a newer revision is still being created.
	LatestReadyRevision string
	// LatestCreatedRevision is the name of the most recently created
	// revision, which might not be Ready yet.
	LatestCreatedRevision string
	// Revisions holds the service's current traffic split, one entry per
	// revision currently receiving traffic (or tagged for direct access).
	Revisions []Revision
}

// Revision represents one revision of a Cloud Run service. Name/Image/Created
// come from Revisions.List (the full revision history); Percent/Latest/Tag
// come from the service's Status.Traffic and are only set for revisions
// currently receiving traffic or holding a URL tag -- most historical
// revisions have Percent 0 and Latest false.
type Revision struct {
	Name    string
	Percent int64
	// Latest is true if this target tracks the service's latest ready
	// revision rather than pinning a specific revision name.
	Latest bool
	// Tag is the URL tag for this traffic target, if one is set.
	Tag string
	// Image is the container image this revision was created from.
	Image string
	// Created is when this revision was created.
	Created time.Time

	// The fields below are only populated by ListRevisions (which reads the
	// full Revision object), not by the lightweight traffic-target parsing
	// in ListServices -- they're empty/zero on a Revision embedded directly
	// in RunService.Revisions until the revisions view fetches the full list.

	// CPULimit/MemoryLimit are the first container's resource limits, e.g.
	// "1" and "512Mi" (Cloud Run's raw Knative resource-quantity strings).
	CPULimit    string
	MemoryLimit string
	// Concurrency is the max in-flight requests per container instance
	// (RevisionSpec.ContainerConcurrency; 0 means unset/default).
	Concurrency int64
	// TimeoutSeconds is the max request duration (RevisionSpec.TimeoutSeconds).
	TimeoutSeconds int64
	// MinScale/MaxScale come from the "autoscaling.knative.dev/{min,max}Scale"
	// revision template annotations; empty if unset.
	MinScale string
	MaxScale string
	// EnvVars lists the first container's environment variable names only
	// (not values, since some may be sourced from Secret Manager).
	EnvVars []string
	// VolumeCount is the number of volumes mounted (secrets/config, if any).
	VolumeCount int
	// VPCConnector comes from the "run.googleapis.com/vpc-access-connector"
	// annotation; empty if unset.
	VPCConnector string
	// ServiceAccount is the revision's runtime identity
	// (RevisionSpec.ServiceAccountName); empty means the project default.
	ServiceAccount string
	// ImageDigest is the resolved digest for Image (RevisionStatus.ImageDigest).
	ImageDigest string
	// Conditions summarizes RevisionStatus.Conditions as "Type: Status" pairs
	// (e.g. "Ready: True", "Active: False").
	Conditions []string
}

// ServiceUpdateOpts holds the fields the Update form can change on an
// existing Cloud Run service's revision template; deploying the change
// creates a new revision. Every field is a string and "" means "leave
// unchanged" (matching TriggerUpdateOpts's convention in the cloudbuild
// package) -- Concurrency/TimeoutSeconds are kept as strings rather than
// int64 so an unset field can't be confused with an explicit 0, which is
// itself a meaningful value for both.
type ServiceUpdateOpts struct {
	Image          string
	CPULimit       string
	MemoryLimit    string
	Concurrency    string
	TimeoutSeconds string
	MinScale       string
	MaxScale       string
	VPCConnector   string
	ServiceAccount string
	// EnvVars is "KEY=value,KEY2=value2". If set, it replaces the container's
	// entire env list with plain values only -- any env var currently backed
	// by a Secret Manager reference on the revision is dropped unless it's
	// re-specified here as a literal value, since secret values are never
	// read back into this form.
	EnvVars string
}

// IAMBinding is one role -> members grant from a service's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}

// TrafficSplitEntry pairs a revision name with the percentage of traffic it
// should receive, for an N-way traffic split (`gcloud run services
// update-traffic --to-revisions=REV1=P1,REV2=P2,...`).
type TrafficSplitEntry struct {
	RevisionName string
	Percent      int64
}
