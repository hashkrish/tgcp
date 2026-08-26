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
