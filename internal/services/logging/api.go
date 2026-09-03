package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
)

// Regex patterns for log cleaning
var (
	// Matches RFC3339-like timestamps at start of line
	// e.g. 2026-01-08T13:51:34.702339+00:00
	reTimestamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T\s]\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})?\s+`)

	// Matches Syslog headers
	// e.g. aryaka-kubeadm kubelet[1213]:
	reSyslogHeader = regexp.MustCompile(`^(\S+\s+)?\S+\[\d+\]:\s+`)

	// Matches standard Go/K8s log prefixes
	// e.g. I0108 13:51:34.701761    1213 scope.go:117]
	reK8sHeader = regexp.MustCompile(`^[IVWE]\d{4}\s+\d{2}:\d{2}:\d{2}\.\d+\s+\d+\s+\S+:\d+\]\s+`)

	// Matches ANSI/VT100 CSI escape sequences (cursor movement, erase-line,
	// color, etc.) -- e.g. "\x1b[2K", "\x1b[1A", "\x1b[37m", "\x1b[?25l".
	// Docker/buildkit's TTY progress output is full of these (used to redraw
	// and reposition several in-flight layer-status lines in place). Passed
	// through unstripped, they get interpreted by whatever terminal is
	// actually rendering tgcp's own output, moving its cursor around and
	// visually corrupting/truncating text that has nothing to do with the
	// log line itself.
	reAnsiEscape = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
)

// Client wraps the Cloud Logging API (v2 REST)
type Client struct {
	service   *logging.Service
	projectID string
}

// NewClient initializes a new Logging client using the v2 REST API. The
// Admin scope (superset of Read) is required now that this client also
// manages sinks/metrics/buckets/views, not just log entries.
func NewClient(ctx context.Context, projectID string) (*Client, error) {
	if demo.Enabled {
		return &Client{projectID: projectID}, nil
	}
	svc, err := logging.NewService(ctx, option.WithScopes(logging.LoggingAdminScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create logging service: %w", err)
	}

	return &Client{
		service:   svc,
		projectID: projectID,
	}, nil
}

// Close is a no-op for the REST service wrapper
func (c *Client) Close() error {
	return nil
}

// ListEntries fetches log entries with pagination
func (c *Client) ListEntries(
	ctx context.Context,
	filter string,
	pageSize int,
	pageToken string,
) ([]LogEntry, string, error) {

	if demo.Enabled {
		return []LogEntry{}, "", nil
	}

	// If no filter is provided, we default to NO filter, relying on OrderBy to get latest.
	// Previously we forced timestamp >= 30m ago, which hid older "latest" logs.
	finalFilter := filter

	// Prepare request
	req := c.service.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + c.projectID},
		Filter:        finalFilter,
		PageSize:      int64(pageSize),
		PageToken:     pageToken,
		OrderBy:       "timestamp desc", // Equivalent to NewestFirst
	})

	resp, err := req.Context(ctx).Do()
	if err != nil {
		return nil, "", fmt.Errorf("failed to list entries: %w", err)
	}

	return parseLogEntries(resp.Entries), resp.NextPageToken, nil
}

// maxLiveEntries caps how many newly-arrived entries a single live-tail poll
// fetches, so a burst of log lines between polls can't stall the UI --
// anything past the cap is picked up on the next poll instead.
const maxLiveEntries = 500

// ListEntriesSince fetches entries matching filter at or after since
// (inclusive), oldest-first, for live-tail polling (see Service.SetLive).
// Inclusive rather than "newer than" so entries sharing since's exact
// timestamp can't be silently skipped if a previous poll didn't return all
// of them (e.g. cut off by maxLiveEntries, or same-timestamp ordering ties)
// -- the caller (Service's liveEntriesMsg handling) is responsible for
// de-duplicating against what it already appended for that timestamp. If
// since is zero, fetches from the beginning of the filter's matching
// history -- used for a live tail's initial batch (e.g. a build's full log
// so far), which is safely bounded since the filter already scopes to one
// resource (e.g. one build ID), not the whole project's logs.
func (c *Client) ListEntriesSince(ctx context.Context, filter string, since time.Time) ([]LogEntry, error) {
	if demo.Enabled {
		return nil, nil
	}

	finalFilter := filter
	if !since.IsZero() {
		finalFilter = fmt.Sprintf(`%s AND timestamp >= "%s"`, filter, since.UTC().Format(time.RFC3339Nano))
	}

	req := c.service.Entries.List(&logging.ListLogEntriesRequest{
		ResourceNames: []string{"projects/" + c.projectID},
		Filter:        finalFilter,
		PageSize:      maxLiveEntries,
		OrderBy:       "timestamp asc",
	})

	resp, err := req.Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list entries: %w", err)
	}

	return parseLogEntries(resp.Entries), nil
}

// parseLogEntries converts raw API log entries into the UI-facing LogEntry
// type, shared by ListEntries and ListEntriesSince.
func parseLogEntries(rawEntries []*logging.LogEntry) []LogEntry {
	var entries []LogEntry
	for _, entry := range rawEntries {
		// Parse Timestamp
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		// Try fallback if Nano fails
		if ts.IsZero() {
			ts, _ = time.Parse(time.RFC3339, entry.Timestamp)
		}

		// Determine Payload and Severity. fullPayload mirrors payload except
		// when there's no extracted message text and we're showing the raw
		// JSON/proto payload verbatim -- there it's indented for readability
		// in the (unconstrained-width) entry detail view, while payload
		// stays compact for the table's single-line Message column.
		payload := ""
		fullPayload := ""
		severity := strings.ToUpper(entry.Severity)

		if entry.TextPayload != "" {
			payload = cleanPayload(entry.TextPayload, ts)
			fullPayload = payload
		} else if len(entry.JsonPayload) > 0 {
			var data map[string]interface{}
			if err := json.Unmarshal(entry.JsonPayload, &data); err == nil {
				// Extract Severity from JSON if missing
				if severity == "" {
					if v, ok := data["severity"].(string); ok {
						severity = strings.ToUpper(v)
					}
				}

				// extract useful message
				if msg, ok := data["message"].(string); ok {
					payload = cleanPayload(msg, ts)
					fullPayload = payload
				} else if msg, ok := data["msg"].(string); ok {
					payload = cleanPayload(msg, ts)
					fullPayload = payload
				} else if msg, ok := data["log"].(string); ok {
					payload = cleanPayload(msg, ts)
					fullPayload = payload
				} else {
					// Fallback to raw JSON string
					payload = string(entry.JsonPayload)
					fullPayload = prettyJSON(entry.JsonPayload)
				}
			} else {
				// Fallback to string
				payload = string(entry.JsonPayload)
				fullPayload = prettyJSON(entry.JsonPayload)
			}
		} else if len(entry.ProtoPayload) > 0 {
			b, _ := json.Marshal(entry.ProtoPayload)
			payload = string(b)
			fullPayload = prettyJSON(b)
		}

		// Extract Resource Info
		var (
			resourceType string
			resourceName string
			location     string
			projID       string
		)

		if entry.Resource != nil {
			resourceType = entry.Resource.Type
			if entry.Resource.Labels != nil {
				projID = entry.Resource.Labels["project_id"]
				location = entry.Resource.Labels["zone"]
				if location == "" {
					location = entry.Resource.Labels["location"]
				}

				switch resourceType {
				case "gce_instance":
					resourceName = entry.Resource.Labels["instance_id"]
				case "cloud_run_revision":
					resourceName = entry.Resource.Labels["service_name"]
				case "k8s_container":
					resourceName = fmt.Sprintf("%s/%s", entry.Resource.Labels["namespace_name"], entry.Resource.Labels["container_name"])
				default:
					for _, v := range entry.Resource.Labels {
						resourceName = v
						break
					}
				}
			}
		}

		if !isValidSeverity(severity) {
			severity = "DEFAULT"
		}

		entries = append(entries, LogEntry{
			Timestamp: ts,
			Severity:  severity,
			Payload:   payload,

			ResourceType: resourceType,
			ResourceName: resourceName,
			Location:     location,
			ProjectID:    projID,

			LogName:     entry.LogName,
			Labels:      entry.Labels,
			InsertID:    entry.InsertId,
			FullPayload: fullPayload,
		})
	}

	return entries
}

// StripTerminalControlChars removes ANSI CSI escape sequences (cursor
// movement, erase-line, color) and normalizes carriage returns to newlines,
// so raw terminal/TTY-oriented text -- e.g. a Cloud Build step's raw stdout,
// full of Docker/buildkit progress redraws -- can be displayed safely
// outside a real terminal. Exported for reuse by other services that show
// raw log text directly (see internal/services/cloudbuild's build-log tail,
// which reads this from the build's GCS log object rather than through
// Cloud Logging entries -- Cloud Logging's own ingestion of TTY-heavy build
// step output already collapses "\r" redraws into truncated fragments
// *before* this package ever sees them, which no amount of client-side
// cleaning here can undo).
func StripTerminalControlChars(raw string) string {
	raw = reAnsiEscape.ReplaceAllString(raw, "")
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return raw
}

// prettyJSON re-encodes raw JSON bytes indented and with object keys sorted
// alphabetically at every nesting level (encoding/json's map[string]any
// marshaling always sorts keys, which json.Indent alone -- a pure
// re-whitespacing pass that preserves original key order -- does not) for
// display in the entry detail view. Falls back to the raw bytes verbatim if
// they don't parse (defensive -- they're already known-valid JSON from the
// API response, but this keeps display code from ever erroring out on that
// assumption).
func prettyJSON(raw []byte) string {
	var data interface{}
	if err := json.Unmarshal(raw, &data); err != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// cleanPayload removes redundant timestamps and prefixes using regex
func cleanPayload(raw string, ts time.Time) string {
	raw = StripTerminalControlChars(raw)

	// 1. Remove Timestamp
	// If the line starts with a timestamp string that looks like our log timestamp, strip it.
	// We rely on Regex for general shape match.
	if loc := reTimestamp.FindStringIndex(raw); loc != nil {
		raw = raw[loc[1]:]
	}

	// 2. Remove Syslog Header
	// e.g. "host app[123]: "
	if loc := reSyslogHeader.FindStringIndex(raw); loc != nil {
		raw = raw[loc[1]:]
	}

	// 3. Remove K8s Header
	// e.g. "I0108 ... ] "
	if loc := reK8sHeader.FindStringIndex(raw); loc != nil {
		raw = raw[loc[1]:]
	}

	// 4. Remove quote wrapper if present (common in "msg" fields)
	if strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		raw = strings.Trim(raw, "\"")
	}

	return strings.TrimSpace(raw)
}

func isValidSeverity(s string) bool {
	switch s {
	case "DEFAULT", "DEBUG", "INFO", "NOTICE", "WARNING", "ERROR", "CRITICAL", "ALERT", "EMERGENCY":
		return true
	}
	return false
}

// -----------------------------------------------------------------------------
// Sinks
// -----------------------------------------------------------------------------

// ListSinks lists all log sinks (export destinations) in the project,
// matching `gcloud logging sinks list`.
func (c *Client) ListSinks() ([]Sink, error) {
	if demo.Enabled {
		return []Sink{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", c.projectID)
	resp, err := c.service.Projects.Sinks.List(parent).Do()
	if err != nil {
		return nil, fmt.Errorf("list sinks: %w", err)
	}
	out := make([]Sink, 0, len(resp.Sinks))
	for _, sk := range resp.Sinks {
		out = append(out, Sink{Name: sk.Name, Destination: sk.Destination, Filter: sk.Filter, Disabled: sk.Disabled})
	}
	return out, nil
}

// CreateSink creates a log sink, matching `gcloud logging sinks create`.
func (c *Client) CreateSink(name, destination, filter string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", c.projectID)
	_, err := c.service.Projects.Sinks.Create(parent, &logging.LogSink{
		Name:        name,
		Destination: destination,
		Filter:      filter,
	}).Do()
	return err
}

// DeleteSink deletes a log sink, matching `gcloud logging sinks delete`.
func (c *Client) DeleteSink(name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	fqName := fmt.Sprintf("projects/%s/sinks/%s", c.projectID, name)
	_, err := c.service.Projects.Sinks.Delete(fqName).Do()
	return err
}

// -----------------------------------------------------------------------------
// Log-based Metrics
// -----------------------------------------------------------------------------

// ListLogMetrics lists all logs-based metrics in the project, matching
// `gcloud logging metrics list`.
func (c *Client) ListLogMetrics() ([]LogMetric, error) {
	if demo.Enabled {
		return []LogMetric{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", c.projectID)
	resp, err := c.service.Projects.Metrics.List(parent).Do()
	if err != nil {
		return nil, fmt.Errorf("list log metrics: %w", err)
	}
	out := make([]LogMetric, 0, len(resp.Metrics))
	for _, m := range resp.Metrics {
		out = append(out, LogMetric{Name: m.Name, Description: m.Description, Filter: m.Filter})
	}
	return out, nil
}

// CreateLogMetric creates a counter-type logs-based metric, matching the
// simplest shape of `gcloud logging metrics create` (a bare log-entry
// filter with no value extractor, label extractors, or distribution
// bucket options).
func (c *Client) CreateLogMetric(name, description, filter string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", c.projectID)
	_, err := c.service.Projects.Metrics.Create(parent, &logging.LogMetric{
		Name:        name,
		Description: description,
		Filter:      filter,
	}).Do()
	return err
}

// DeleteLogMetric deletes a logs-based metric, matching
// `gcloud logging metrics delete`.
func (c *Client) DeleteLogMetric(name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	fqName := fmt.Sprintf("projects/%s/metrics/%s", c.projectID, name)
	_, err := c.service.Projects.Metrics.Delete(fqName).Do()
	return err
}

// -----------------------------------------------------------------------------
// Log Buckets
// -----------------------------------------------------------------------------

// ListLogBuckets lists all log buckets in the given location ("global" for
// the default/common case, or a region), matching `gcloud logging buckets
// list`.
func (c *Client) ListLogBuckets(location string) ([]LogBucket, error) {
	if demo.Enabled {
		return []LogBucket{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", c.projectID, location)
	resp, err := c.service.Projects.Locations.Buckets.List(parent).Do()
	if err != nil {
		return nil, fmt.Errorf("list log buckets: %w", err)
	}
	out := make([]LogBucket, 0, len(resp.Buckets))
	for _, b := range resp.Buckets {
		shortName := b.Name
		if idx := strings.LastIndex(b.Name, "/"); idx != -1 {
			shortName = b.Name[idx+1:]
		}
		out = append(out, LogBucket{Name: shortName, FullName: b.Name, RetentionDays: b.RetentionDays, Locked: b.Locked})
	}
	return out, nil
}

// CreateLogBucket creates a log bucket in the given location, matching
// `gcloud logging buckets create`.
func (c *Client) CreateLogBucket(location, bucketID string, retentionDays int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", c.projectID, location)
	call := c.service.Projects.Locations.Buckets.Create(parent, &logging.LogBucket{RetentionDays: retentionDays})
	call.BucketId(bucketID)
	_, err := call.Do()
	return err
}

// DeleteLogBucket deletes a log bucket, matching `gcloud logging buckets
// delete`. GCP soft-deletes buckets for 7 days before permanent removal
// (recoverable via `gcloud logging buckets undelete` in that window).
func (c *Client) DeleteLogBucket(location, bucketID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	fqName := fmt.Sprintf("projects/%s/locations/%s/buckets/%s", c.projectID, location, bucketID)
	_, err := c.service.Projects.Locations.Buckets.Delete(fqName).Do()
	return err
}

// -----------------------------------------------------------------------------
// Log Views
// -----------------------------------------------------------------------------

// ListLogViews lists all views on a log bucket, matching `gcloud logging
// views list`.
func (c *Client) ListLogViews(location, bucketID string) ([]LogView, error) {
	if demo.Enabled {
		return []LogView{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s/buckets/%s", c.projectID, location, bucketID)
	resp, err := c.service.Projects.Locations.Buckets.Views.List(parent).Do()
	if err != nil {
		return nil, fmt.Errorf("list log views: %w", err)
	}
	out := make([]LogView, 0, len(resp.Views))
	for _, v := range resp.Views {
		shortName := v.Name
		if idx := strings.LastIndex(v.Name, "/"); idx != -1 {
			shortName = v.Name[idx+1:]
		}
		out = append(out, LogView{Name: shortName, FullName: v.Name, Filter: v.Filter})
	}
	return out, nil
}

// CreateLogView creates a view on a log bucket, matching `gcloud logging
// views create`.
func (c *Client) CreateLogView(location, bucketID, viewID, filter string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s/buckets/%s", c.projectID, location, bucketID)
	call := c.service.Projects.Locations.Buckets.Views.Create(parent, &logging.LogView{Filter: filter})
	call.ViewId(viewID)
	_, err := call.Do()
	return err
}

// DeleteLogView deletes a view on a log bucket, matching `gcloud logging
// views delete`.
func (c *Client) DeleteLogView(fullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	_, err := c.service.Projects.Locations.Buckets.Views.Delete(fullName).Do()
	return err
}

// GetLogViewIAMPolicy reads a view's current IAM policy, matching `gcloud
// logging views get-iam-policy`. Used as the "look before you grant" read
// step before AddLogViewIAMBinding.
func (c *Client) GetLogViewIAMPolicy(fullName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("logging client not initialized")
	}
	policy, err := c.service.Projects.Locations.Buckets.Views.GetIamPolicy(fullName, &logging.GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, fmt.Errorf("get view IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddLogViewIAMBinding grants a role to a member on a log view, matching
// `gcloud logging views add-iam-policy-binding`. It fetches the current
// policy, merges the new binding into it, and writes the whole policy
// back — this never drops any existing binding, unlike a raw
// set-iam-policy (deliberately not implemented; see repo Notes on
// lockout risk).
func (c *Client) AddLogViewIAMBinding(fullName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("logging client not initialized")
	}
	policy, err := c.service.Projects.Locations.Buckets.Views.GetIamPolicy(fullName, &logging.GetIamPolicyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("get view IAM policy: %w", err)
	}
	policy.Bindings = mergeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Projects.Locations.Buckets.Views.SetIamPolicy(fullName, &logging.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeIAMBinding appends member to the existing binding for role if one
// exists (skipping if already granted), or appends a brand-new role
// binding otherwise. It never removes or replaces any other binding.
func mergeIAMBinding(bindings []*logging.Binding, role, member string) []*logging.Binding {
	for _, b := range bindings {
		if b.Role != role {
			continue
		}
		for _, m := range b.Members {
			if m == member {
				return bindings
			}
		}
		b.Members = append(b.Members, member)
		return bindings
	}
	return append(bindings, &logging.Binding{Role: role, Members: []string{member}})
}
