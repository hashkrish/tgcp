package cloudbuild

import (
	"context"
	"fmt"
	"strings"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/iterator"
)

// maxBuilds caps how many recent builds we fetch, to keep the listing fast
// and avoid pulling a project's entire build history.
const maxBuilds = 100

// Client wraps the real Cloud Build API client.
type Client struct {
	client *cloudbuild.Client
}

// NewClient constructs a Client using Application Default Credentials, the
// same implicit auth flow used by every other service in this repo.
func NewClient(ctx context.Context) (*Client, error) {
	c, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud build client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListBuilds lists the most recent Cloud Build builds for the given project
// (read-only). Builds are returned newest-first, which is the API's default
// ordering.
func (c *Client) ListBuilds(projectID string) ([]BuildItem, error) {
	ctx := context.Background()

	req := &cloudbuildpb.ListBuildsRequest{
		ProjectId: projectID,
		PageSize:  int32(maxBuilds),
	}

	var items []BuildItem
	it := c.client.ListBuilds(ctx, req)
	for len(items) < maxBuilds {
		b, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, toBuildItem(b))
	}

	return items, nil
}

// CreateBuild submits ("gcloud builds submit" equivalent) a new build
// consisting of a single build step. This is a minimal-viable create form —
// no source archive/repo is wired up, just a single step image + args and an
// optional image to be produced, which is enough to construct a valid Build
// message without a full YAML build config editor.
func (c *Client) CreateBuild(projectID string, opts BuildCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}

	build := &cloudbuildpb.Build{
		Steps: []*cloudbuildpb.BuildStep{
			{
				Name: opts.StepImage,
				Args: splitAndTrim(opts.StepArgs),
			},
		},
	}
	if opts.ImageName != "" {
		build.Images = []string{opts.ImageName}
	}
	if subs := parseSubstitutions(opts.Substitutions); len(subs) > 0 {
		build.Substitutions = subs
	}

	ctx := context.Background()
	// Fire-and-forget: this is a long-running operation; don't block on it
	// completing — the next list refresh will reflect it once done.
	_, err := c.client.CreateBuild(ctx, &cloudbuildpb.CreateBuildRequest{
		ProjectId: projectID,
		Build:     build,
	})
	return err
}

// RetryBuild triggers a new build that retries the given build's steps with
// the same configuration. This is a long-running operation; the call returns
// as soon as the retry is accepted rather than waiting for it to complete —
// the new build shows up on the next list refresh.
func (c *Client) RetryBuild(projectID, buildID string) error {
	ctx := context.Background()
	_, err := c.client.RetryBuild(ctx, &cloudbuildpb.RetryBuildRequest{
		ProjectId: projectID,
		Id:        buildID,
	})
	return err
}

// CancelBuild cancels an in-progress build.
func (c *Client) CancelBuild(projectID, buildID string) error {
	ctx := context.Background()
	_, err := c.client.CancelBuild(ctx, &cloudbuildpb.CancelBuildRequest{
		ProjectId: projectID,
		Id:        buildID,
	})
	return err
}

// splitAndTrim splits a comma-separated string into trimmed, non-empty parts.
func splitAndTrim(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseSubstitutions parses a "KEY=value,KEY2=value2" free-text field into a
// substitutions map. Entries that don't contain "=" are skipped.
func parseSubstitutions(s string) map[string]string {
	if s == "" {
		return nil
	}
	out := make(map[string]string)
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out
}

// toBuildItem maps a Build proto into the UI-facing BuildItem.
func toBuildItem(b *cloudbuildpb.Build) BuildItem {
	var createTime, startTime, finishTime time.Time
	if t := b.GetCreateTime(); t != nil {
		createTime = t.AsTime()
	}
	if t := b.GetStartTime(); t != nil {
		startTime = t.AsTime()
	}
	if t := b.GetFinishTime(); t != nil {
		finishTime = t.AsTime()
	}

	var duration time.Duration
	if !startTime.IsZero() && !finishTime.IsZero() {
		duration = finishTime.Sub(startTime)
	}

	return BuildItem{
		ID:             b.GetId(),
		Status:         b.GetStatus().String(),
		StatusDetail:   b.GetStatusDetail(),
		TriggerID:      b.GetBuildTriggerId(),
		CreateTime:     createTime,
		StartTime:      startTime,
		FinishTime:     finishTime,
		Duration:       duration,
		LogURL:         b.GetLogUrl(),
		Images:         b.GetImages(),
		Source:         formatSource(b.GetSource()),
		ServiceAccount: b.GetServiceAccount(),
		LogsBucket:     b.GetLogsBucket(),
		Tags:           b.GetTags(),
		Substitutions:  b.GetSubstitutions(),
	}
}

// formatSource renders a Build's source location as a short human-readable string.
func formatSource(src *cloudbuildpb.Source) string {
	if src == nil {
		return ""
	}
	if repo := src.GetRepoSource(); repo != nil {
		ref := repo.GetBranchName()
		if ref == "" {
			ref = repo.GetTagName()
		}
		if ref == "" {
			ref = repo.GetCommitSha()
		}
		if ref != "" {
			return fmt.Sprintf("%s@%s", repo.GetRepoName(), ref)
		}
		return repo.GetRepoName()
	}
	if storage := src.GetStorageSource(); storage != nil {
		return fmt.Sprintf("gs://%s/%s", storage.GetBucket(), storage.GetObject())
	}
	return ""
}
