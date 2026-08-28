package monitoring

// UptimeCheckCreateOpts holds the minimal set of fields needed to create an
// HTTP(S) Uptime check via the Create form.
type UptimeCheckCreateOpts struct {
	DisplayName      string
	Host             string
	Path             string
	CheckIntervalSec string // numeric string
	Protocol         string // HTTP or HTTPS
}

// UptimeCheck represents a Cloud Monitoring Uptime check configuration
// (read-only view).
type UptimeCheck struct {
	Name         string // Short check ID
	FullName     string // Full resource name (projects/*/uptimeCheckConfigs/*)
	DisplayName  string
	ResourceType string // Monitored resource type, e.g. uptime_url, gce_instance
	CheckType    string // HTTP, HTTPS, TCP
	Host         string // Target host, from the monitored resource's labels
	Path         string // HTTP(S) path, empty for TCP
	Port         int32
	Period       string // e.g. "60s"
	Timeout      string // e.g. "10s"

	ContentMatchers []ContentMatcher
}

// ContentMatcher describes a single Uptime check content matcher.
type ContentMatcher struct {
	Content string // The literal/regex/JSON content being matched (not sensitive)
	Matcher string // Matcher kind, e.g. CONTAINS_STRING, MATCHES_REGEX
}

// AlertPolicy represents a Cloud Monitoring alerting policy (read-only view).
type AlertPolicy struct {
	Name        string // Short policy ID
	FullName    string // projects/{project}/alertPolicies/{id}
	DisplayName string
	Enabled     bool
	Combiner    string // AND, OR, AND_WITH_MATCHING_RESOURCE

	Conditions               []AlertCondition
	NotificationChannelCount int
}

// AlertCondition describes a single condition within an alert policy.
// Notification channel contact details are intentionally never surfaced
// here — only a count of channels is shown at the policy level.
type AlertCondition struct {
	DisplayName string
	Kind        string // Metric Threshold, Metric Absence, Log Match, MQL, PromQL, SQL, Unknown

	// Populated for Metric Threshold / Metric Absence conditions; empty otherwise.
	MetricFilter string
	Comparison   string // >, <, >=, <=, ==, != (empty if not applicable)
	Threshold    string // formatted threshold value
	Duration     string // e.g. "300s"
}
