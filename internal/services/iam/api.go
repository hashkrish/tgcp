package iam

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/api/option"
)

// Client handles IAM API interactions
type Client struct {
	service   *iam.Service
	resmgrSvc *cloudresourcemanager.Service
}

// NewClient creates a new IAM API client
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	httpClient, err := core.NewHTTPClient(ctx, iam.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}

	service, err := iam.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create iam service: %w", err)
	}

	resmgrSvc, err := cloudresourcemanager.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create resource manager service: %w", err)
	}

	return &Client{service: service, resmgrSvc: resmgrSvc}, nil
}

// ListServiceAccounts lists all service accounts in the project
// resource should be "projects/PROJECT_ID"
func (c *Client) ListServiceAccounts(projectID string) ([]ServiceAccount, error) {
	if demo.Enabled {
		return []ServiceAccount{}, nil
	}
	resource := "projects/" + projectID
	resp, err := c.service.Projects.ServiceAccounts.List(resource).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to list service accounts: %w", err)
	}

	var accounts []ServiceAccount
	for _, acc := range resp.Accounts {
		accounts = append(accounts, ServiceAccount{
			Name:        acc.Name,
			Email:       acc.Email,
			DisplayName: acc.DisplayName,
			Description: acc.Description,
			Disabled:    acc.Disabled,
			UniqueID:    acc.UniqueId,
		})
	}
	return accounts, nil
}

// GetProjectPolicyBindings fetches the project's IAM policy and flattens its
// role bindings into one PolicyMember per (role, member) pair.
func (c *Client) GetProjectPolicyBindings(projectID string) ([]PolicyMember, error) {
	if demo.Enabled {
		return []PolicyMember{}, nil
	}
	policy, err := c.resmgrSvc.Projects.GetIamPolicy(projectID, &cloudresourcemanager.GetIamPolicyRequest{}).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get project IAM policy: %w", err)
	}

	var bindings []PolicyMember
	for _, b := range policy.Bindings {
		for _, m := range b.Members {
			bindings = append(bindings, PolicyMember{Role: b.Role, Member: m})
		}
	}
	return bindings, nil
}

// CreateServiceAccount creates a new IAM service account with the given
// account ID (the local part of the eventual email address) and an
// optional display name.
func (c *Client) CreateServiceAccount(projectID, accountID, displayName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	resource := "projects/" + projectID
	req := &iam.CreateServiceAccountRequest{
		AccountId: accountID,
	}
	if displayName != "" {
		req.ServiceAccount = &iam.ServiceAccount{DisplayName: displayName}
	}
	_, err := c.service.Projects.ServiceAccounts.Create(resource, req).Do()
	return err
}

// DeleteServiceAccount deletes an IAM service account, matching
// `gcloud iam service-accounts delete`. This immediately breaks any
// workload authenticating as this identity (there is a short undelete
// window in the real API, but this app doesn't expose `undelete`).
func (c *Client) DeleteServiceAccount(resourceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	_, err := c.service.Projects.ServiceAccounts.Delete(resourceName).Do()
	return err
}

// DisableServiceAccount disables a service account, matching
// `gcloud iam service-accounts disable`. A disabled account can no longer
// authenticate but can be re-enabled at any time (unlike delete).
func (c *Client) DisableServiceAccount(resourceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	_, err := c.service.Projects.ServiceAccounts.Disable(resourceName, &iam.DisableServiceAccountRequest{}).Do()
	return err
}

// EnableServiceAccount re-enables a previously disabled service account,
// matching `gcloud iam service-accounts enable`.
func (c *Client) EnableServiceAccount(resourceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	_, err := c.service.Projects.ServiceAccounts.Enable(resourceName, &iam.EnableServiceAccountRequest{}).Do()
	return err
}

// UpdateServiceAccountDisplayName patches a service account's display name,
// matching `gcloud iam service-accounts update --display-name`. Description
// and the `undelete`/`keys` operations are out of scope for this minimal
// Update flow — `undelete` specifically needs the deleted account's unique
// ID within a 30-day window, which this app doesn't track since it doesn't
// list deleted accounts.
func (c *Client) UpdateServiceAccountDisplayName(resourceName, displayName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	req := &iam.PatchServiceAccountRequest{
		ServiceAccount: &iam.ServiceAccount{
			Name:        resourceName,
			DisplayName: displayName,
		},
		UpdateMask: "displayName",
	}
	_, err := c.service.Projects.ServiceAccounts.Patch(resourceName, req).Do()
	return err
}

// UndeleteServiceAccount restores a recently-deleted service account,
// matching `gcloud iam service-accounts undelete`. Real gcloud looks up the
// account's unique ID from `service-accounts list --show-deleted` first;
// this app doesn't track deleted accounts (they simply vanish from
// ListServiceAccounts), so the caller supplies the unique ID directly --
// only recoverable within GCP's ~30-day undelete window.
func (c *Client) UndeleteServiceAccount(projectID, uniqueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	resourceName := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectID, uniqueID)
	_, err := c.service.Projects.ServiceAccounts.Undelete(resourceName, &iam.UndeleteServiceAccountRequest{}).Do()
	return err
}

// GetServiceAccountIAMPolicy reads a service account's own IAM policy --
// who can act as/impersonate this identity (e.g. roles/iam.serviceAccountUser),
// as opposed to GetProjectPolicyBindings' project-level policy of what this
// identity itself can do. Matches `gcloud iam service-accounts get-iam-policy`.
func (c *Client) GetServiceAccountIAMPolicy(resourceName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("iam client not initialized")
	}
	policy, err := c.service.Projects.ServiceAccounts.GetIamPolicy(resourceName).Do()
	if err != nil {
		return nil, fmt.Errorf("get service account IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddServiceAccountIAMBinding grants a role to a member on a service
// account's own IAM policy, matching
// `gcloud iam service-accounts add-iam-policy-binding`. It fetches the
// current policy, merges the new binding in, and writes the whole policy
// back -- this never drops any existing binding, unlike a raw
// set-iam-policy (which this app deliberately never calls).
func (c *Client) AddServiceAccountIAMBinding(resourceName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	policy, err := c.service.Projects.ServiceAccounts.GetIamPolicy(resourceName).Do()
	if err != nil {
		return fmt.Errorf("get service account IAM policy: %w", err)
	}
	policy.Bindings = mergeServiceAccountIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Projects.ServiceAccounts.SetIamPolicy(resourceName, &iam.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeServiceAccountIAMBinding appends member to the existing binding for
// role if one exists (skipping if already granted), or appends a brand-new
// role binding otherwise. It never removes or replaces any other binding.
func mergeServiceAccountIAMBinding(bindings []*iam.Binding, role, member string) []*iam.Binding {
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
	return append(bindings, &iam.Binding{Role: role, Members: []string{member}})
}

// ListServiceAccountKeys lists the user-managed keys on a service account,
// matching `gcloud iam service-accounts keys list --managed-by=user`.
// System-managed keys are excluded -- they're auto-rotated by Google and
// can't be created/deleted through the API.
func (c *Client) ListServiceAccountKeys(resourceName string) ([]ServiceAccountKey, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("iam client not initialized")
	}
	resp, err := c.service.Projects.ServiceAccounts.Keys.List(resourceName).KeyTypes("USER_MANAGED").Do()
	if err != nil {
		return nil, fmt.Errorf("list service account keys: %w", err)
	}
	var keys []ServiceAccountKey
	for _, k := range resp.Keys {
		keys = append(keys, ServiceAccountKey{
			Name:            k.Name,
			KeyID:           shortKeyID(k.Name),
			ValidAfterTime:  k.ValidAfterTime,
			ValidBeforeTime: k.ValidBeforeTime,
			Disabled:        k.Disabled,
		})
	}
	return keys, nil
}

func shortKeyID(fullName string) string {
	const marker = "/keys/"
	if i := strings.LastIndex(fullName, marker); i >= 0 {
		return fullName[i+len(marker):]
	}
	return fullName
}

// CreateServiceAccountKey creates a new user-managed key for a service
// account and writes the returned private key JSON to outputPath, matching
// `gcloud iam service-accounts keys create OUTPUT_FILE`. The private key is
// only ever returned once by the API -- if the write fails, the key still
// exists server-side and the caller must delete it manually.
func (c *Client) CreateServiceAccountKey(resourceName, outputPath string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	key, err := c.service.Projects.ServiceAccounts.Keys.Create(resourceName, &iam.CreateServiceAccountKeyRequest{}).Do()
	if err != nil {
		return fmt.Errorf("create service account key: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(key.PrivateKeyData)
	if err != nil {
		return fmt.Errorf("key %s was created but its key data could not be decoded -- delete it manually: %w", key.Name, err)
	}
	if err := os.WriteFile(outputPath, data, 0600); err != nil {
		return fmt.Errorf("key %s was created but could not be written to %s -- delete it manually: %w", key.Name, outputPath, err)
	}
	return nil
}

// DeleteServiceAccountKey deletes a user-managed key, matching
// `gcloud iam service-accounts keys delete`.
func (c *Client) DeleteServiceAccountKey(keyResourceName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("iam client not initialized")
	}
	_, err := c.service.Projects.ServiceAccounts.Keys.Delete(keyResourceName).Do()
	return err
}
