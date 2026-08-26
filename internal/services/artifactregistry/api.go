package artifactregistry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
)

// maxConcurrentLocationFetches caps how many per-location ListRepositories
// calls run in parallel when fanning out across a project's regions.
const maxConcurrentLocationFetches = 10

// Client wraps the real Artifact Registry API client.
type Client struct {
	client *artifactregistry.Client
}

// NewClient constructs a Client using Application Default Credentials, the
// same implicit auth flow used by every other service in this repo.
func NewClient(ctx context.Context) (*Client, error) {
	c, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("artifact registry client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListRepositories lists Artifact Registry repositories across all regions
// for the given project (read-only).
//
// The Artifact Registry v1 API does not support a "-" wildcard location for
// an aggregated ListRepositories call (unlike some other GCP APIs), so this
// first discovers the project's visible locations via ListLocations, then
// fans out a ListRepositories call per location, matching what `gcloud
// artifacts repositories list` does under the hood.
func (c *Client) ListRepositories(projectID string) ([]RepositoryItem, error) {
	ctx := context.Background()

	locations, err := c.listLocations(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var (
		mu    sync.Mutex
		items []RepositoryItem
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentLocationFetches)

	for _, loc := range locations {
		loc := loc
		g.Go(func() error {
			parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
			req := &artifactregistrypb.ListRepositoriesRequest{Parent: parent}

			var found []RepositoryItem
			it := c.client.ListRepositories(gctx, req)
			for {
				repo, err := it.Next()
				if err == iterator.Done {
					break
				}
				if err != nil {
					return fmt.Errorf("list repositories in %s: %w", loc, err)
				}
				found = append(found, toRepositoryItem(repo))
			}

			mu.Lock()
			items = append(items, found...)
			mu.Unlock()
			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return items, nil
}

// listLocations returns the location IDs (e.g. "us-central1") visible to
// this project for Artifact Registry.
func (c *Client) listLocations(ctx context.Context, projectID string) ([]string, error) {
	req := &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}

	var locations []string
	it := c.client.ListLocations(ctx, req)
	for {
		loc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list locations: %w", err)
		}
		locations = append(locations, loc.GetLocationId())
	}
	return locations, nil
}

// toRepositoryItem maps a Repository proto into the UI-facing RepositoryItem.
func toRepositoryItem(repo *artifactregistrypb.Repository) RepositoryItem {
	// Repo full name looks like:
	// projects/{project}/locations/{location}/repositories/{repository}
	id := repo.GetName()
	region := ""
	parts := strings.Split(repo.GetName(), "/")
	for i := 0; i < len(parts)-1; i++ {
		switch parts[i] {
		case "locations":
			region = parts[i+1]
		case "repositories":
			id = parts[i+1]
		}
	}

	var created time.Time
	if ct := repo.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}

	return RepositoryItem{
		Name:        id,
		Format:      repo.GetFormat().String(),
		Mode:        repo.GetMode().String(),
		Region:      region,
		SizeBytes:   repo.GetSizeBytes(),
		Description: repo.GetDescription(),
		CreateTime:  created,
	}
}
