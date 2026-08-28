package parametermanager

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	parametermanager "google.golang.org/api/parametermanager/v1"
)

// defaultLocation is the location used for the "global" Parameter Manager
// resources. Parameter Manager also supports regional parameters, but the
// global location covers the common case and mirrors how most GCP configs
// are stored today.
const defaultLocation = "global"

// Client wraps the Parameter Manager API
type Client struct {
	service *parametermanager.Service
}

// NewClient creates a new Parameter Manager client
func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := parametermanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create parametermanager service: %w", err)
	}
	return &Client{service: svc}, nil
}

// ListParameters returns all parameters in the project's global location
func (c *Client) ListParameters(projectID string) ([]Parameter, error) {
	if demo.Enabled {
		return []Parameter{}, nil
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, defaultLocation)
	var params []Parameter

	req := c.service.Projects.Locations.Parameters.List(parent)
	err := req.Pages(context.Background(), func(resp *parametermanager.ListParametersResponse) error {
		for _, p := range resp.Parameters {
			param := Parameter{
				FullName: p.Name,
				Name:     extractParameterName(p.Name),
				Format:   p.Format,
				Labels:   p.Labels,
			}
			if param.Format == "" {
				param.Format = "UNFORMATTED"
			}

			if p.CreateTime != "" {
				if t, err := time.Parse(time.RFC3339Nano, p.CreateTime); err == nil {
					param.CreateTime = t
				}
			}

			params = append(params, param)
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list parameters: %w", err)
	}

	return params, nil
}

// ListVersions returns all versions for a parameter
func (c *Client) ListVersions(parameterName string) ([]ParameterVersion, error) {
	if demo.Enabled {
		return []ParameterVersion{}, nil
	}
	var versions []ParameterVersion

	req := c.service.Projects.Locations.Parameters.Versions.List(parameterName)
	err := req.Pages(context.Background(), func(resp *parametermanager.ListParameterVersionsResponse) error {
		for _, v := range resp.ParameterVersions {
			version := ParameterVersion{
				FullName: v.Name,
				Name:     extractVersionID(v.Name),
				Disabled: v.Disabled,
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
		return nil, fmt.Errorf("failed to list parameter versions: %w", err)
	}

	return versions, nil
}

// GetVersionPayload fetches the value of a specific parameter version.
// Parameter Manager values are non-secret config by design, so unlike
// Secret Manager there is no reveal-gating — this is called as soon as a
// version is selected.
func (c *Client) GetVersionPayload(versionFullName string) (string, error) {
	if demo.Enabled {
		return "", nil
	}
	if c.service == nil {
		return "", fmt.Errorf("client not initialized")
	}

	resp, err := c.service.Projects.Locations.Parameters.Versions.Get(versionFullName).Do()
	if err != nil {
		return "", fmt.Errorf("failed to get parameter version: %w", err)
	}
	if resp.Payload == nil {
		return "", nil
	}
	// The wire representation of the payload is base64 (proto `bytes` field
	// serialized to JSON); the generated client leaves it as a raw string,
	// so decode it here to get the actual value.
	decoded, err := base64.StdEncoding.DecodeString(resp.Payload.Data)
	if err != nil {
		return resp.Payload.Data, nil
	}
	return string(decoded), nil
}

// CreateParameter creates a new Parameter Manager parameter (metadata only
// — no version/value is set here; adding a version is a separate,
// out-of-scope data-plane operation) in the project's global location.
func (c *Client) CreateParameter(projectID, parameterID, format string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("parametermanager client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, defaultLocation)
	if format == "" {
		format = "UNFORMATTED"
	}
	param := &parametermanager.Parameter{
		Format: format,
	}
	_, err := c.service.Projects.Locations.Parameters.Create(parent, param).ParameterId(parameterID).Do()
	return err
}

// UpdateParameterLabels replaces a parameter's labels via a labels-only
// Patch, matching `gcloud parametermanager parameters update
// --update-labels`. Format is immutable after creation and versions
// create/render are out of scope for this minimal Update flow.
func (c *Client) UpdateParameterLabels(parameterName string, labels map[string]string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("parametermanager client not initialized")
	}
	param := &parametermanager.Parameter{Labels: labels}
	_, err := c.service.Projects.Locations.Parameters.Patch(parameterName, param).UpdateMask("labels").Do()
	return err
}

// DeleteParameter deletes a Parameter Manager parameter and all of its
// versions, matching `gcloud parametermanager parameters delete`.
func (c *Client) DeleteParameter(parameterName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("parametermanager client not initialized")
	}
	_, err := c.service.Projects.Locations.Parameters.Delete(parameterName).Do()
	return err
}

// extractParameterName extracts the short parameter name from the full
// resource name, e.g. "projects/p/locations/global/parameters/x" -> "x"
func extractParameterName(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 6 {
		return parts[5]
	}
	return fullName
}

// extractVersionID extracts the version id from the full resource name,
// e.g. "projects/p/locations/global/parameters/x/versions/1" -> "1"
func extractVersionID(fullName string) string {
	parts := strings.Split(fullName, "/")
	if len(parts) >= 8 {
		return parts[7]
	}
	return fullName
}
