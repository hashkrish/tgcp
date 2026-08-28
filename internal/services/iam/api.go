package iam

import (
	"context"
	"fmt"

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
