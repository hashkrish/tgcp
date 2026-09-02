package cloudbuild

import (
	"context"
	"fmt"
	"strings"
	"time"

	cloudbuild "cloud.google.com/go/cloudbuild/apiv1/v2"
	"cloud.google.com/go/cloudbuild/apiv1/v2/cloudbuildpb"
	cloudbuildv2 "cloud.google.com/go/cloudbuild/apiv2"
	cloudbuildpbv2 "cloud.google.com/go/cloudbuild/apiv2/cloudbuildpb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/iterator"
)

// maxBuilds caps how many recent builds we fetch, to keep the listing fast
// and avoid pulling a project's entire build history.
const maxBuilds = 100

// Client wraps the real Cloud Build API client(s). repoMgr is the 2nd-gen
// "Repository Manager" client (Connections/Repositories, GitHub App-backed
// source integrations) -- a separate API surface from the 1st-gen client
// used for builds/triggers/worker-pools.
type Client struct {
	client  *cloudbuild.Client
	repoMgr *cloudbuildv2.RepositoryManagerClient
}

// NewClient constructs a Client using Application Default Credentials, the
// same implicit auth flow used by every other service in this repo.
func NewClient(ctx context.Context) (*Client, error) {
	c, err := cloudbuild.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud build client: %w", err)
	}
	repoMgr, err := cloudbuildv2.NewRepositoryManagerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud build repository manager client: %w", err)
	}
	return &Client{client: c, repoMgr: repoMgr}, nil
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

// -----------------------------------------------------------------------------
// Build Triggers
// -----------------------------------------------------------------------------

// ListBuildTriggers lists the build triggers configured for the project.
func (c *Client) ListBuildTriggers(projectID string) ([]TriggerItem, error) {
	ctx := context.Background()
	req := &cloudbuildpb.ListBuildTriggersRequest{ProjectId: projectID}

	var items []TriggerItem
	it := c.client.ListBuildTriggers(ctx, req)
	for {
		t, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, toTriggerItem(t))
	}
	return items, nil
}

// TriggerCreateOpts holds the minimal set of fields needed to create a
// repo-based build trigger: a branch-name regex on a Cloud Source
// Repository, building from a cloudbuild.yaml file in the matched source.
// GitHub-App-backed and Pub/Sub/webhook trigger types are out of scope for
// this minimal Create flow.
type TriggerCreateOpts struct {
	Name            string
	RepoName        string // Cloud Source Repository name
	BranchPattern   string // regex, e.g. "^main$"
	BuildConfigPath string // path to the build config file, e.g. "cloudbuild.yaml"
}

// CreateBuildTrigger creates a repo-based build trigger, matching
// `gcloud builds triggers create cloud-source-repositories`.
func (c *Client) CreateBuildTrigger(projectID string, opts TriggerCreateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	_, err := c.client.CreateBuildTrigger(ctx, &cloudbuildpb.CreateBuildTriggerRequest{
		ProjectId: projectID,
		Trigger: &cloudbuildpb.BuildTrigger{
			Name: opts.Name,
			TriggerTemplate: &cloudbuildpb.RepoSource{
				RepoName: opts.RepoName,
				Revision: &cloudbuildpb.RepoSource_BranchName{BranchName: opts.BranchPattern},
			},
			BuildTemplate: &cloudbuildpb.BuildTrigger_Filename{Filename: opts.BuildConfigPath},
		},
	})
	return err
}

// RunBuildTrigger manually invokes a trigger against its configured branch,
// matching `gcloud builds triggers run`. repoName is required by the API
// alongside branchName -- a RepoSource with only a branch name (no repo_name)
// is rejected as INVALID_ARGUMENT. Triggers with no Cloud Source Repository
// (e.g. GitHub App-backed 2nd-gen triggers) have no repoName to give, so for
// those the trigger is run with no override source, using its own configured
// source instead.
func (c *Client) RunBuildTrigger(projectID, triggerID, repoName, branchName string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	req := &cloudbuildpb.RunBuildTriggerRequest{
		ProjectId: projectID,
		TriggerId: triggerID,
	}
	if repoName != "" {
		req.Source = &cloudbuildpb.RepoSource{
			RepoName: repoName,
			Revision: &cloudbuildpb.RepoSource_BranchName{BranchName: branchName},
		}
	}
	_, err := c.client.RunBuildTrigger(ctx, req)
	return err
}

// DeleteBuildTrigger deletes a build trigger, matching `gcloud builds triggers delete`.
func (c *Client) DeleteBuildTrigger(projectID, triggerID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	err := c.client.DeleteBuildTrigger(ctx, &cloudbuildpb.DeleteBuildTriggerRequest{
		ProjectId: projectID,
		TriggerId: triggerID,
	})
	return err
}

// TriggerUpdateOpts holds the fields the Edit form / enable-disable action
// can change on a trigger. Empty string / nil means "leave unchanged".
type TriggerUpdateOpts struct {
	Description     string
	BranchPattern   string
	BuildConfigPath string
	Disabled        *bool
}

// UpdateBuildTrigger updates a build trigger, matching `gcloud builds
// triggers update`. UpdateBuildTriggerRequest requires the whole BuildTrigger
// message (not a true field-mask patch client-side), so this fetches the
// current trigger first and mutates only the requested fields -- mirroring
// Cloud Run's Get-then-mutate-then-Replace UpdateServiceImage pattern -- to
// avoid clobbering fields this form doesn't expose (Tags, Substitutions,
// GitHub config, etc.).
func (c *Client) UpdateBuildTrigger(projectID, triggerID string, opts TriggerUpdateOpts) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	cur, err := c.client.GetBuildTrigger(ctx, &cloudbuildpb.GetBuildTriggerRequest{
		ProjectId: projectID,
		TriggerId: triggerID,
	})
	if err != nil {
		return fmt.Errorf("get trigger: %w", err)
	}
	if opts.Description != "" {
		cur.Description = opts.Description
	}
	if opts.BranchPattern != "" && cur.GetTriggerTemplate() != nil {
		cur.TriggerTemplate.Revision = &cloudbuildpb.RepoSource_BranchName{BranchName: opts.BranchPattern}
	}
	if opts.BuildConfigPath != "" {
		cur.BuildTemplate = &cloudbuildpb.BuildTrigger_Filename{Filename: opts.BuildConfigPath}
	}
	if opts.Disabled != nil {
		cur.Disabled = *opts.Disabled
	}
	_, err = c.client.UpdateBuildTrigger(ctx, &cloudbuildpb.UpdateBuildTriggerRequest{
		ProjectId: projectID,
		TriggerId: triggerID,
		Trigger:   cur,
	})
	return err
}

// toTriggerItem maps a BuildTrigger proto into the UI-facing TriggerItem.
func toTriggerItem(t *cloudbuildpb.BuildTrigger) TriggerItem {
	var created time.Time
	if ct := t.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}
	repoName := ""
	branch := ""
	if tpl := t.GetTriggerTemplate(); tpl != nil {
		repoName = tpl.GetRepoName()
		branch = tpl.GetBranchName()
	}
	return TriggerItem{
		ID:              t.GetId(),
		Name:            t.GetName(),
		Description:     t.GetDescription(),
		RepoName:        repoName,
		BranchName:      branch,
		BuildConfigPath: t.GetFilename(),
		Tags:            t.GetTags(),
		Substitutions:   t.GetSubstitutions(),
		Disabled:        t.GetDisabled(),
		CreateTime:      created,
	}
}

// -----------------------------------------------------------------------------
// Worker Pools (private pools; regional resources)
// -----------------------------------------------------------------------------

// ListWorkerPools lists the private worker pools in the given region.
func (c *Client) ListWorkerPools(projectID, region string) ([]WorkerPoolItem, error) {
	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	req := &cloudbuildpb.ListWorkerPoolsRequest{Parent: parent}

	var items []WorkerPoolItem
	it := c.client.ListWorkerPools(ctx, req)
	for {
		wp, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, toWorkerPoolItem(wp))
	}
	return items, nil
}

// CreateWorkerPool creates a private worker pool with default machine/network
// configuration, matching a bare `gcloud builds worker-pools create`
// (no --peered-network / custom machine type — those are left at the
// service's defaults for this minimal Create flow).
func (c *Client) CreateWorkerPool(projectID, region, poolID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	op, err := c.client.CreateWorkerPool(ctx, &cloudbuildpb.CreateWorkerPoolRequest{
		Parent:       parent,
		WorkerPoolId: poolID,
		WorkerPool: &cloudbuildpb.WorkerPool{
			Config: &cloudbuildpb.WorkerPool_PrivatePoolV1Config{
				PrivatePoolV1Config: &cloudbuildpb.PrivatePoolV1Config{},
			},
		},
	})
	if err != nil {
		return err
	}
	// Fire-and-forget: don't block on the long-running create completing --
	// the next list refresh will reflect it once done.
	_ = op
	return nil
}

// DeleteWorkerPool deletes a private worker pool, matching
// `gcloud builds worker-pools delete`.
func (c *Client) DeleteWorkerPool(projectID, region, poolID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	name := fmt.Sprintf("projects/%s/locations/%s/workerPools/%s", projectID, region, poolID)
	_, err := c.client.DeleteWorkerPool(ctx, &cloudbuildpb.DeleteWorkerPoolRequest{Name: name})
	return err
}

// toWorkerPoolItem maps a WorkerPool proto into the UI-facing WorkerPoolItem.
func toWorkerPoolItem(wp *cloudbuildpb.WorkerPool) WorkerPoolItem {
	var created time.Time
	if ct := wp.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}
	return WorkerPoolItem{
		Name:        wp.GetName(),
		DisplayName: wp.GetDisplayName(),
		State:       wp.GetState().String(),
		CreateTime:  created,
	}
}

// -----------------------------------------------------------------------------
// Connections & Repositories (2nd-gen source integrations)
// -----------------------------------------------------------------------------

// ListConnections lists the 2nd-gen source repository connections
// (GitHub/GitLab/Bitbucket App installations) in the given region.
func (c *Client) ListConnections(projectID, region string) ([]ConnectionItem, error) {
	ctx := context.Background()
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	req := &cloudbuildpbv2.ListConnectionsRequest{Parent: parent}

	var items []ConnectionItem
	it := c.repoMgr.ListConnections(ctx, req)
	for {
		conn, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, toConnectionItem(conn))
	}
	return items, nil
}

// DeleteConnection deletes a 2nd-gen connection, matching
// `gcloud builds connections delete`. Creating a connection is deliberately
// not supported: it requires completing an interactive GitHub/GitLab/
// Bitbucket App installation OAuth flow in a browser, which this TUI has no
// way to drive -- connections must be created via the Cloud Console or
// `gcloud builds connections create` first.
func (c *Client) DeleteConnection(projectID, region, connectionID string) error {
	if demo.Enabled {
		return nil
	}
	if c.repoMgr == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	name := fmt.Sprintf("projects/%s/locations/%s/connections/%s", projectID, region, connectionID)
	_, err := c.repoMgr.DeleteConnection(ctx, &cloudbuildpbv2.DeleteConnectionRequest{Name: name})
	return err
}

// AddConnectionIAMBinding grants a role to a member on a connection,
// matching `gcloud builds connections add-iam-policy-binding`. It fetches
// the current policy, merges the new binding into it, and writes the whole
// policy back -- this never drops any existing binding, unlike a raw
// set-iam-policy.
func (c *Client) AddConnectionIAMBinding(connFullName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.repoMgr == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	policy, err := c.repoMgr.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{Resource: connFullName})
	if err != nil {
		return fmt.Errorf("get connection IAM policy: %w", err)
	}
	policy.Bindings = mergeConnectionIAMBinding(policy.GetBindings(), role, member)
	_, err = c.repoMgr.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{Resource: connFullName, Policy: policy})
	return err
}

// mergeConnectionIAMBinding appends member to the existing binding for role
// if one exists (skipping if already granted), or appends a brand-new role
// binding otherwise. It never removes or replaces any other binding.
func mergeConnectionIAMBinding(bindings []*iampb.Binding, role, member string) []*iampb.Binding {
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

// toConnectionItem maps a Connection proto into the UI-facing ConnectionItem.
func toConnectionItem(conn *cloudbuildpbv2.Connection) ConnectionItem {
	var created time.Time
	if ct := conn.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}
	provider := "unknown"
	switch {
	case conn.GetGithubConfig() != nil:
		provider = "GitHub"
	case conn.GetGithubEnterpriseConfig() != nil:
		provider = "GitHub Enterprise"
	case conn.GetGitlabConfig() != nil:
		provider = "GitLab"
	case conn.GetBitbucketCloudConfig() != nil:
		provider = "Bitbucket Cloud"
	case conn.GetBitbucketDataCenterConfig() != nil:
		provider = "Bitbucket Data Center"
	}
	return ConnectionItem{
		Name:       conn.GetName(),
		Provider:   provider,
		Disabled:   conn.GetDisabled(),
		CreateTime: created,
	}
}

// ListCBRepositories lists the repositories linked to a 2nd-gen connection.
func (c *Client) ListCBRepositories(connFullName string) ([]CBRepositoryItem, error) {
	ctx := context.Background()
	req := &cloudbuildpbv2.ListRepositoriesRequest{Parent: connFullName}

	var items []CBRepositoryItem
	it := c.repoMgr.ListRepositories(ctx, req)
	for {
		repo, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		items = append(items, toCBRepositoryItem(repo))
	}
	return items, nil
}

// DeleteCBRepository deletes a linked repository, matching
// `gcloud builds repositories delete`. Creating a repository link is
// deliberately not supported here for the same reason as connection
// creation -- see DeleteConnection's doc comment.
func (c *Client) DeleteCBRepository(repoFullName string) error {
	if demo.Enabled {
		return nil
	}
	if c.repoMgr == nil {
		return fmt.Errorf("client not initialized")
	}
	ctx := context.Background()
	_, err := c.repoMgr.DeleteRepository(ctx, &cloudbuildpbv2.DeleteRepositoryRequest{Name: repoFullName})
	return err
}

// toCBRepositoryItem maps a Repository proto into the UI-facing CBRepositoryItem.
func toCBRepositoryItem(repo *cloudbuildpbv2.Repository) CBRepositoryItem {
	var created time.Time
	if ct := repo.GetCreateTime(); ct != nil {
		created = ct.AsTime()
	}
	return CBRepositoryItem{
		Name:       repo.GetName(),
		RemoteURI:  repo.GetRemoteUri(),
		CreateTime: created,
	}
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
