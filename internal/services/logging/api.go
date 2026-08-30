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

	var entries []LogEntry
	for _, entry := range resp.Entries {
		// Parse Timestamp
		ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
		// Try fallback if Nano fails
		if ts.IsZero() {
			ts, _ = time.Parse(time.RFC3339, entry.Timestamp)
		}

		// Determine Payload and Severity
		payload := ""
		severity := strings.ToUpper(entry.Severity)

		if entry.TextPayload != "" {
			payload = cleanPayload(entry.TextPayload, ts)
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
				} else if msg, ok := data["msg"].(string); ok {
					payload = cleanPayload(msg, ts)
				} else if msg, ok := data["log"].(string); ok {
					payload = cleanPayload(msg, ts)
				} else {
					// Fallback to raw JSON string
					payload = string(entry.JsonPayload)
				}
			} else {
				// Fallback to string
				payload = string(entry.JsonPayload)
			}
		} else if len(entry.ProtoPayload) > 0 {
			b, _ := json.Marshal(entry.ProtoPayload)
			payload = string(b)
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
			FullPayload: payload, // Simplified for now
		})
	}

	return entries, resp.NextPageToken, nil
}

// cleanPayload removes redundant timestamps and prefixes using regex
func cleanPayload(raw string, ts time.Time) string {
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
