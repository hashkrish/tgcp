package iam

// ServiceAccount represents a Google Cloud Service Account
type ServiceAccount struct {
	Name        string `json:"name"`
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	UniqueID    string `json:"uniqueId"`
}

// PolicyMember represents a member in an IAM policy
type PolicyMember struct {
	Role   string
	Member string // user:email, serviceAccount:email, etc
}

// IAMBinding is one role -> members grant on a service account's own IAM
// policy (i.e. who can act as/impersonate this identity), distinct from
// PolicyMember which is the project's policy (what this identity can do).
type IAMBinding struct {
	Role    string
	Members []string
}

// ServiceAccountKey represents one user-managed key on a service account
// (`gcloud iam service-accounts keys list`). System-managed keys are
// omitted -- they can't be created/deleted via the API, only listed, and
// aren't actionable from this app.
type ServiceAccountKey struct {
	Name            string // full resource name, .../keys/{key_id}
	KeyID           string
	ValidAfterTime  string
	ValidBeforeTime string
	Disabled        bool
}
