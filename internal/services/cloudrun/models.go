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
}

// TrafficSplitEntry pairs a revision name with the percentage of traffic it
// should receive, for an N-way traffic split (`gcloud run services
// update-traffic --to-revisions=REV1=P1,REV2=P2,...`).
type TrafficSplitEntry struct {
	RevisionName string
	Percent      int64
}
