package cloudfunctions

import "time"

// Function represents a Cloud Function (Gen1 or Gen2) as returned by the
// Cloud Functions v2 API. The v2 API surfaces both generations via the
// Environment field ("GEN_1" / "GEN_2"), so a single client covers both —
// see api.go's package comment for details.
type Function struct {
	Name        string // short name, e.g. "my-function"
	FullName    string // fully qualified: projects/{p}/locations/{l}/functions/{n}
	Region      string
	Environment string // GEN_1 or GEN_2
	State       string
	Runtime     string

	TriggerType   string // HTTP, Pub/Sub, Cloud Storage, Firestore, Audit Log, Eventarc, Unknown
	TriggerDetail string // topic name / bucket name / event type, depending on TriggerType

	Memory string // e.g. "256M"
	CPU    string // e.g. "1" (vCPU)

	EntryPoint     string
	SourceLocation string // human-readable source description (bucket/object, repo, or git URI)

	// EnvVarCount is the count of runtime environment variables configured
	// on the function. We deliberately never fetch or render the actual
	// values here — some commonly hold secrets.
	EnvVarCount int

	URL        string // HTTP endpoint, if any (Gen2 services / HTTP-triggered functions)
	UpdateTime time.Time
}

// IAMBinding is one role -> members grant from a function's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}
