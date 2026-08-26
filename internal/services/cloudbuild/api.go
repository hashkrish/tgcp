package cloudbuild

import (
	"context"
	"fmt"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
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
		ID:           b.GetId(),
		Status:       b.GetStatus().String(),
		StatusDetail: b.GetStatusDetail(),
		TriggerID:    b.GetBuildTriggerId(),
		CreateTime:   createTime,
		StartTime:    startTime,
		FinishTime:   finishTime,
		Duration:     duration,
		LogURL:       b.GetLogUrl(),
		Images:       b.GetImages(),
	}
}
