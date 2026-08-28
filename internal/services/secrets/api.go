package secrets

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	secretmanager "google.golang.org/api/secretmanager/v1"
)

// Client wraps the Secret Manager API
type Client struct {
	service *secretmanager.Service
}

// NewClient creates a new Secret Manager client
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := secretmanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create secretmanager service: %w", err)
	}
	return &Client{service: svc}, nil
}

// ListSecrets returns all secrets in the project
func (c *Client) ListSecrets(projectID string) ([]Secret, error) {
	if demo.Enabled {
		return []Secret{}, nil
	}
	parent := fmt.Sprintf("projects/%s", projectID)
	var secrets []Secret

	req := c.service.Projects.Secrets.List(parent)
	err := req.Pages(context.Background(), func(resp *secretmanager.ListSecretsResponse) error {
		for _, s := range resp.Secrets {
			secret := Secret{
				FullName:    s.Name,
				Name:        extractSecretName(s.Name),
				Labels:      s.Labels,
				Replication: formatReplication(s.Replication),
			}

			// Parse create time
			if s.CreateTime != "" {
				if t, err := time.Parse(time.RFC3339Nano, s.CreateTime); err == nil {
					secret.CreateTime = t
				}
			}

			secrets = append(secrets, secret)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	return secrets, nil
}

// GetSecret returns details for a specific secret including version count
func (c *Client) GetSecret(secretName string) (*Secret, error) {
	if demo.Enabled {
		return &Secret{}, nil
	}
	resp, err := c.service.Projects.Secrets.Get(secretName).Do()
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	secret := &Secret{
		FullName:    resp.Name,
		Name:        extractSecretName(resp.Name),
		Labels:      resp.Labels,
		Replication: formatReplication(resp.Replication),
	}

	if resp.CreateTime != "" {
		if t, err := time.Parse(time.RFC3339Nano, resp.CreateTime); err == nil {
			secret.CreateTime = t
		}
	}

	return secret, nil
}

// ListVersions returns all versions for a secret
func (c *Client) ListVersions(secretName string) ([]SecretVersion, error) {
	if demo.Enabled {
		return []SecretVersion{}, nil
	}
	var versions []SecretVersion

	req := c.service.Projects.Secrets.Versions.List(secretName)
	err := req.Pages(context.Background(), func(resp *secretmanager.ListSecretVersionsResponse) error {
		for _, v := range resp.Versions {
			version := SecretVersion{
				FullName: v.Name,
				Name:     extractVersionNumber(v.Name),
				State:    v.State,
			}

			if v.CreateTime != "" {
				if t, err := time.Parse(time.RFC3339Nano, v.CreateTime); err == nil {
					version.CreateTime = t
				}
			}

			versions = append(versions, version)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list versions: %w", err)
	}

	return versions, nil
}

// AccessVersion fetches the actual secret payload for a specific version.
// This is only ever called in direct response to an explicit user action
// (the "reveal" keybinding) — never automatically on navigation.
func (c *Client) AccessVersion(versionName string) (string, error) {
	if demo.Enabled {
		return "demo-secret-value", nil
	}
	if c.service == nil {
		return "", fmt.Errorf("client not initialized")
	}
	resp, err := c.service.Projects.Secrets.Versions.Access(versionName).Do()
	if err != nil {
		return "", fmt.Errorf("failed to access secret version: %w", err)
	}
	if resp.Payload == nil {
		return "", nil
	}
	// The wire representation of the payload is base64 (proto `bytes` field
	// serialized to JSON); the generated client leaves it as a raw string,
	// so decode it here to get the actual secret value.
	decoded, err := base64.StdEncoding.DecodeString(resp.Payload.Data)
	if err != nil {
		// Not valid base64 for some reason — fall back to the raw string
		// rather than failing the reveal outright.
		return resp.Payload.Data, nil
	}
	return string(decoded), nil
}

// AddVersion adds a new version to a secret with the given plaintext
// payload, matching `gcloud secrets versions add`. It returns the newly
// created version's short name (e.g. "3").
func (c *Client) AddVersion(secretName, value string) (string, error) {
	if demo.Enabled {
		return "demo", nil
	}
	if c.service == nil {
		return "", fmt.Errorf("secretmanager client not initialized")
	}
	req := &secretmanager.AddSecretVersionRequest{
		Payload: &secretmanager.SecretPayload{
			Data: base64.StdEncoding.EncodeToString([]byte(value)),
		},
	}
	resp, err := c.service.Projects.Secrets.AddVersion(secretName, req).Do()
	if err != nil {
		return "", fmt.Errorf("failed to add secret version: %w", err)
	}
	return extractVersionNumber(resp.Name), nil
}

// DisableVersion disables a secret version, matching
// `gcloud secrets versions disable`. A disabled version can no longer be
// accessed but can be re-enabled later (unlike destroy).
func (c *Client) DisableVersion(versionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	_, err := c.service.Projects.Secrets.Versions.Disable(versionName, &secretmanager.DisableSecretVersionRequest{}).Do()
	return err
}

// EnableVersion re-enables a previously disabled secret version, matching
// `gcloud secrets versions enable`.
func (c *Client) EnableVersion(versionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	_, err := c.service.Projects.Secrets.Versions.Enable(versionName, &secretmanager.EnableSecretVersionRequest{}).Do()
	return err
}

// DestroyVersion irrecoverably destroys a secret version's payload, matching
// `gcloud secrets versions destroy`. Unlike disable, this cannot be undone.
func (c *Client) DestroyVersion(versionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	_, err := c.service.Projects.Secrets.Versions.Destroy(versionName, &secretmanager.DestroySecretVersionRequest{}).Do()
	return err
}

// extractSecretName extracts the secret name from the full resource name
// e.g., "projects/my-project/secrets/my-secret" -> "my-secret"
func extractSecretName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 4 {
		return parts[3]
	}
	return fullName
}

// extractVersionNumber extracts version number from full resource name
// e.g., "projects/my-project/secrets/my-secret/versions/1" -> "1"
func extractVersionNumber(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 6 {
		return parts[5]
	}
	return fullName
}

// CreateSecret creates a new Secret Manager secret (metadata only — no
// version/value is set here; adding a version is a separate, out-of-scope
// data-plane operation). replication is either "automatic" (the common
// case) or a comma-separated list of regions for user-managed replication.
func (c *Client) CreateSecret(projectID, secretID, replication string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	parent := fmt.Sprintf("projects/%s", projectID)

	repl := &secretmanager.Replication{Automatic: &secretmanager.Automatic{}}
	if replication != "" && replication != "automatic" {
		var replicas []*secretmanager.Replica
		for _, region := range strings.Split(replication, ",") {
			region = strings.TrimSpace(region)
			if region == "" {
				continue
			}
			replicas = append(replicas, &secretmanager.Replica{Location: region})
		}
		if len(replicas) > 0 {
			repl = &secretmanager.Replication{UserManaged: &secretmanager.UserManaged{Replicas: replicas}}
		}
	}

	secret := &secretmanager.Secret{Replication: repl}
	_, err := c.service.Projects.Secrets.Create(parent, secret).SecretId(secretID).Do()
	return err
}

// UpdateSecretLabels replaces a secret's labels via a labels-only Patch,
// matching `gcloud secrets update --update-labels`. Replication policy
// changes are a separate, more involved operation and are out of scope for
// this minimal Update flow.
func (c *Client) UpdateSecretLabels(secretName string, labels map[string]string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	secret := &secretmanager.Secret{Labels: labels}
	_, err := c.service.Projects.Secrets.Patch(secretName, secret).UpdateMask("labels").Do()
	return err
}

// GetSecretIAMPolicy reads a secret's current IAM policy, matching
// `gcloud secrets get-iam-policy`. Used as the "look before you grant" read
// step before AddSecretIAMBinding.
func (c *Client) GetSecretIAMPolicy(secretName string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("secretmanager client not initialized")
	}
	policy, err := c.service.Projects.Secrets.GetIamPolicy(secretName).Do()
	if err != nil {
		return nil, fmt.Errorf("get secret IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddSecretIAMBinding grants a role to a member on a secret, matching
// `gcloud secrets add-iam-policy-binding`. It fetches the current policy,
// merges the new binding into it, and writes the whole policy back — this
// never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddSecretIAMBinding(secretName, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	policy, err := c.service.Projects.Secrets.GetIamPolicy(secretName).Do()
	if err != nil {
		return fmt.Errorf("get secret IAM policy: %w", err)
	}
	policy.Bindings = mergeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Projects.Secrets.SetIamPolicy(secretName, &secretmanager.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeIAMBinding appends member to the existing binding for role if one
// exists (skipping if already granted), or appends a brand-new role binding
// otherwise. It never removes or replaces any other binding in the slice.
func mergeIAMBinding(bindings []*secretmanager.Binding, role, member string) []*secretmanager.Binding {
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
	return append(bindings, &secretmanager.Binding{Role: role, Members: []string{member}})
}

// DeleteSecret deletes a Secret Manager secret and all of its versions,
// matching `gcloud secrets delete`.
func (c *Client) DeleteSecret(secretName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("secretmanager client not initialized")
	}
	_, err := c.service.Projects.Secrets.Delete(secretName).Do()
	return err
}

// formatReplication formats the replication config for display
func formatReplication(r *secretmanager.Replication) string {
	if r == nil {
		return "unknown"
	}
	if r.Automatic != nil {
		return "automatic"
	}
	if r.UserManaged != nil && len(r.UserManaged.Replicas) > 0 {
		var regions []string
		for _, replica := range r.UserManaged.Replicas {
			regions = append(regions, replica.Location)
		}
		return strings.Join(regions, ", ")
	}
	return "unknown"
}
