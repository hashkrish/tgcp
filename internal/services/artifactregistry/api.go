package artifactregistry

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"

	"golang.org/x/sync/errgroup"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
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

// CreateRepository creates a new Artifact Registry repository. This is a
// long-running operation; the call returns as soon as the create is accepted
// rather than waiting for it to complete — the new repository shows up on
// the next list refresh.
func (c *Client) CreateRepository(projectID string, opts RepositoryCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}

	format, ok := artifactregistrypb.Repository_Format_value[strings.ToUpper(opts.Format)]
	if !ok {
		return fmt.Errorf("invalid format %q", opts.Format)
	}

	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, opts.Location)

	_, err := c.client.CreateRepository(ctx, &artifactregistrypb.CreateRepositoryRequest{
		Parent:       parent,
		RepositoryId: opts.RepositoryID,
		Repository: &artifactregistrypb.Repository{
			Format:      artifactregistrypb.Repository_Format(format),
			Description: opts.Description,
		},
	})
	return err
}

// UpdateRepositoryDescription patches a repository's description, matching
// `gcloud artifacts repositories update --description`. set-cleanup-policies
// and other repository fields are out of scope for this minimal Update flow.
func (c *Client) UpdateRepositoryDescription(projectID, location, repositoryID, description string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/repositories/%s", projectID, location, repositoryID)
	_, err := c.client.UpdateRepository(context.Background(), &artifactregistrypb.UpdateRepositoryRequest{
		Repository: &artifactregistrypb.Repository{
			Name:        name,
			Description: description,
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"description"}},
	})
	return err
}

// DeleteRepository deletes an Artifact Registry repository and everything
// in it (all images/packages/versions), matching
// `gcloud artifacts repositories delete`.
func (c *Client) DeleteRepository(projectID, location, repositoryID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/repositories/%s", projectID, location, repositoryID)
	_, err := c.client.DeleteRepository(context.Background(), &artifactregistrypb.DeleteRepositoryRequest{Name: name})
	return err
}

// GetRepositoryIAMPolicy reads a repository's current IAM policy, matching
// `gcloud artifacts repositories get-iam-policy`. Used as the "look before
// you grant" read step before AddRepositoryIAMBinding.
func (c *Client) GetRepositoryIAMPolicy(repoFullName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not initialized")
	}
	policy, err := c.client.GetIamPolicy(context.Background(), &iampb.GetIamPolicyRequest{Resource: repoFullName})
	if err != nil {
		return nil, fmt.Errorf("get repository IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.GetBindings() {
		out = append(out, IAMBinding{Role: b.GetRole(), Members: b.GetMembers()})
	}
	return out, nil
}

// AddRepositoryIAMBinding grants a role to a member on a repository,
// matching `gcloud artifacts repositories add-iam-policy-binding`. It
// fetches the current policy, merges the new binding into it, and writes
// the whole policy back — this never drops any existing binding, unlike a
// raw set-iam-policy.
func (c *Client) AddRepositoryIAMBinding(repoFullName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	policy, err := c.client.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{Resource: repoFullName})
	if err != nil {
		return fmt.Errorf("get repository IAM policy: %w", err)
	}
	policy.Bindings = mergeIAMBinding(policy.GetBindings(), role, member)
	_, err = c.client.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{Resource: repoFullName, Policy: policy})
	return err
}

// mergeIAMBinding appends member to the existing binding for role if one
// exists (skipping if already granted), or appends a brand-new role binding
// otherwise. It never removes or replaces any other binding in the slice.
func mergeIAMBinding(bindings []*iampb.Binding, role, member string) []*iampb.Binding {
	for _, b := range bindings {
		if b.GetRole() != role {
			continue
		}
		for _, m := range b.GetMembers() {
			if m == member {
				return bindings
			}
		}
		b.Members = append(b.Members, member)
		return bindings
	}
	return append(bindings, &iampb.Binding{Role: role, Members: []string{member}})
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

// ListDockerImages lists the Docker images stored in a single repository.
// repoParent is the full repository resource name
// ("projects/{p}/locations/{l}/repositories/{r}").
func (c *Client) ListDockerImages(repoParent string) ([]DockerImage, error) {
	ctx := context.Background()

	req := &artifactregistrypb.ListDockerImagesRequest{Parent: repoParent}
	var images []DockerImage
	it := c.client.ListDockerImages(ctx, req)
	for {
		img, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("list docker images: %w", err)
		}
		images = append(images, toDockerImage(img))
	}
	return images, nil
}

// DeleteImage deletes a single Docker image (version). imageFullName is the
// full version resource name, e.g.
// "projects/{p}/locations/{l}/repositories/{r}/packages/{pkg}/versions/{digest}".
// Force is set since Artifact Registry images are normally tagged, and an
// untagged delete would otherwise be rejected.
func (c *Client) DeleteImage(imageFullName string) error {
	ctx := context.Background()
	// Fire-and-forget: don't block on the long-running delete completing —
	// the next list refresh will reflect it once done.
	_, err := c.client.DeleteVersion(ctx, &artifactregistrypb.DeleteVersionRequest{
		Name:  imageFullName,
		Force: true,
	})
	return err
}

// toDockerImage maps a DockerImage proto into the UI-facing DockerImage.
func toDockerImage(img *artifactregistrypb.DockerImage) DockerImage {
	// Image name looks like:
	// projects/{p}/locations/{l}/repositories/{r}/dockerImages/{digest}
	// The corresponding Version resource name (used for delete) swaps the
	// "dockerImages" segment for "packages/{pkg}/versions/{digest}" — but
	// Artifact Registry treats the dockerImages name as an acceptable alias
	// for the version name in DeleteVersion, so it's kept as-is.
	var uploadTime, buildTime time.Time
	if t := img.GetUploadTime(); t != nil {
		uploadTime = t.AsTime()
	}
	if t := img.GetBuildTime(); t != nil {
		buildTime = t.AsTime()
	}

	return DockerImage{
		Name:           img.GetName(),
		URI:            img.GetUri(),
		Tags:           img.GetTags(),
		ImageSizeBytes: img.GetImageSizeBytes(),
		UploadTime:     uploadTime,
		BuildTime:      buildTime,
		MediaType:      img.GetMediaType(),
	}
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

	var created, updated time.Time
	if ct := repo.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}
	if ut := repo.GetUpdateTime(); ut != nil {
		updated = ut.AsTime()
	}

	return RepositoryItem{
		Name:                id,
		Format:              repo.GetFormat().String(),
		Mode:                repo.GetMode().String(),
		Region:              region,
		SizeBytes:           repo.GetSizeBytes(),
		Description:         repo.GetDescription(),
		CreateTime:          created,
		UpdateTime:          updated,
		Labels:              repo.GetLabels(),
		KmsKeyName:          repo.GetKmsKeyName(),
		RegistryUri:         repo.GetRegistryUri(),
		CleanupPolicyCount:  len(repo.GetCleanupPolicies()),
		CleanupPolicyDryRun: repo.GetCleanupPolicyDryRun(),
	}
}
