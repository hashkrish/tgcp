// Package cloudfunctions provides a read-only view of Cloud Functions.
//
// Gen1 vs Gen2: this package uses the Cloud Functions v2 REST API
// (google.golang.org/api/cloudfunctions/v2), which despite its name lists
// BOTH generations of functions — each returned Function carries an
// `Environment` field of "GEN_1" or "GEN_2" telling you which. There is no
// need to also call the v1 API; verified via `go doc` (see Function.Environment
// in that package) and confirmed at runtime against a real project containing
// a Gen1 function, which the v2 List call returned correctly. This mirrors
// how internal/services/cloudrun/functions.go already uses this same v2
// client — this package goes further by surfacing per-function metadata
// (trigger type/detail, runtime, memory/CPU, entry point, source location,
// env var count) that the Cloud Run "Functions" tab does not.
//
// No mutating calls are made anywhere in this package — list/get only.
package cloudfunctions

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"
)

type Client struct {
	service *cloudfunctions.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	opts := []option.ClientOption{option.WithScopes(cloudfunctions.CloudPlatformScope)}
	svc, err := cloudfunctions.NewService(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create cloud functions service: %w", err)
	}
	return &Client{service: svc}, nil
}

// ListFunctions lists all Cloud Functions (Gen1 and Gen2) across every
// region, using the "-" wildcard location the v2 API supports for
// aggregated listing (no need for the per-region fan-out pattern used by
// scheduler/cloudtasks, since this API supports it directly).
func (c *Client) ListFunctions(projectID string) ([]Function, error) {
	if demo.Enabled {
		return []Function{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	parent := fmt.Sprintf("projects/%s/locations/-", projectID)

	var results []Function
	err := c.service.Projects.Locations.Functions.List(parent).Pages(context.Background(), func(page *cloudfunctions.ListFunctionsResponse) error {
		for _, f := range page.Functions {
			results = append(results, toFunction(f))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list functions: %w", err)
	}
	return results, nil
}

func toFunction(f *cloudfunctions.Function) Function {
	fn := Function{
		FullName:    f.Name,
		Name:        shortName(f.Name),
		Region:      extractRegion(f.Name),
		Environment: f.Environment,
		State:       f.State,
	}

	if t, err := time.Parse(time.RFC3339, f.UpdateTime); err == nil {
		fn.UpdateTime = t
	}

	if bc := f.BuildConfig; bc != nil {
		fn.Runtime = bc.Runtime
		fn.EntryPoint = bc.EntryPoint
		fn.SourceLocation = describeSource(bc.Source)
	}

	if sc := f.ServiceConfig; sc != nil {
		fn.Memory = sc.AvailableMemory
		fn.CPU = sc.AvailableCpu
		fn.URL = sc.Uri
		fn.EnvVarCount = len(sc.EnvironmentVariables)
	}

	fn.TriggerType, fn.TriggerDetail = describeTrigger(f)

	return fn
}

// describeTrigger classifies a function's trigger into a human-readable
// type and a short detail string (topic name, bucket name, event type,
// etc). A function with no EventTrigger is HTTP-triggered.
func describeTrigger(f *cloudfunctions.Function) (triggerType, detail string) {
	et := f.EventTrigger
	if et == nil {
		return "HTTP", ""
	}

	switch {
	case strings.Contains(et.EventType, "pubsub"):
		return "Pub/Sub", shortName(et.PubsubTopic)
	case strings.Contains(et.EventType, "storage"):
		return "Cloud Storage", eventFilterValue(et.EventFilters, "bucket")
	case strings.Contains(et.EventType, "firestore"):
		return "Firestore", eventFilterValue(et.EventFilters, "database")
	case strings.Contains(et.EventType, "audit"):
		return "Audit Log", et.EventType
	default:
		return "Eventarc", et.EventType
	}
}

func eventFilterValue(filters []*cloudfunctions.EventFilter, attr string) string {
	for _, f := range filters {
		if f.Attribute == attr {
			return f.Value
		}
	}
	return ""
}

func describeSource(src *cloudfunctions.Source) string {
	if src == nil {
		return ""
	}
	switch {
	case src.StorageSource != nil:
		return fmt.Sprintf("gs://%s/%s", src.StorageSource.Bucket, src.StorageSource.Object)
	case src.RepoSource != nil:
		return fmt.Sprintf("repo:%s", src.RepoSource.RepoName)
	case src.GitUri != "":
		return src.GitUri
	default:
		return ""
	}
}

func shortName(fullName string) string {
	parts := strings.Split(fullName, "/")
	return parts[len(parts)-1]
}

// extractRegion pulls the location segment out of a fully qualified
// function name: projects/{project}/locations/{location}/functions/{name}
func extractRegion(fullName string) string {
	parts := strings.Split(fullName, "/")
	for i, p := range parts {
		if p == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
