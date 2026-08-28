package cloudrun

import (
	"context"
	"fmt"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/cloudfunctions/v2"
	"google.golang.org/api/option"
	run "google.golang.org/api/run/v1"
)

type Client struct {
	service   *run.APIService
	functions *cloudfunctions.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	opts := []option.ClientOption{option.WithScopes(run.CloudPlatformScope)}

	svc, err := run.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	funcSvc, err := cloudfunctions.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}

	return &Client{service: svc, functions: funcSvc}, nil
}

func (c *Client) ListServices(projectID string) ([]RunService, error) {
	if demo.Enabled {
		return loadDemoRunServices(), nil
	}
	// List services across all locations ("-")
	parent := fmt.Sprintf("projects/%s/locations/-", projectID)

	resp, err := c.service.Projects.Locations.Services.List(parent).Do()
	if err != nil {
		return nil, err
	}

	var services []RunService
	for _, item := range resp.Items {
		// Parse useful information
		name := item.Metadata.Name
		// Name is often fully qualified, let's extract the simple name if needed?
		// But usually Metadata.Name IS the simple name in the k8s object,
		// but check if it returns standard k8s object.
		// Actually run/v1 returns a Service object where Metadata.Name is usually just "my-service".

		region := "global"
		if item.Metadata.Labels != nil {
			if loc, ok := item.Metadata.Labels["cloud.googleapis.com/location"]; ok {
				region = loc
			}
		}

		status := StatusUnknown
		url := ""
		if item.Status != nil {
			url = item.Status.Url
			for _, cond := range item.Status.Conditions {
				if cond.Type == "Ready" {
					switch cond.Status {
					case "True":
						status = StatusReady
					case "False":
						status = StatusFailed
					}
					break
				}
			}
		}

		image := ""
		if item.Spec != nil && item.Spec.Template != nil && item.Spec.Template.Spec != nil {
			if containers := item.Spec.Template.Spec.Containers; len(containers) > 0 {
				image = containers[0].Image
			}
		}

		services = append(services, RunService{
			Name:   name,
			Region: region,
			URL:    url,
			Status: status,
			Image:  image,
		})
	}
	return services, nil
}

// CreateService creates a new Cloud Run service running a single container
// with an ephemeral revision name (auto-assigned by the API). This
// intentionally omits most of the Knative ServiceSpec's optional fields
// (env vars, resource limits, concurrency, VPC access, ingress, etc.) —
// only the container image and listening port are set, matching the
// minimal viable field set for this Create flow.
func (c *Client) CreateService(projectID, region, name, image string, port int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)

	svc := &run.Service{
		ApiVersion: "serving.knative.dev/v1",
		Kind:       "Service",
		Metadata: &run.ObjectMeta{
			Name:      name,
			Namespace: projectID,
		},
		Spec: &run.ServiceSpec{
			Template: &run.RevisionTemplate{
				Spec: &run.RevisionSpec{
					Containers: []*run.Container{
						{
							Image: image,
							Ports: []*run.ContainerPort{
								{ContainerPort: port},
							},
						},
					},
				},
			},
		},
	}

	_, err := c.service.Projects.Locations.Services.Create(parent, svc).Do()
	return err
}

// UpdateServiceImage patches a single field on an existing Cloud Run
// service: the container image of the first container in the revision
// template. This is the minimal viable "update" flow for Cloud Run —
// update-traffic (traffic split management) and full service replace
// (env vars, resources, concurrency, VPC access, ingress, etc.) are
// explicitly out of scope and skipped.
//
// The run/v1 API has no field-level PATCH for services, so this reads the
// current service, mutates only the image, and calls ReplaceService with
// the full object (as gcloud's `run services update --image` does under
// the hood).
func (c *Client) UpdateServiceImage(projectID, region, name, image string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, name)

	svc, err := c.service.Projects.Locations.Services.Get(fqName).Do()
	if err != nil {
		return err
	}
	if svc.Spec == nil || svc.Spec.Template == nil || svc.Spec.Template.Spec == nil || len(svc.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("service %s has no container spec to update", name)
	}
	svc.Spec.Template.Spec.Containers[0].Image = image

	_, err = c.service.Projects.Locations.Services.ReplaceService(fqName, svc).Do()
	return err
}

// DeleteService deletes a Cloud Run service, matching `gcloud run services delete`.
func (c *Client) DeleteService(projectID, region, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}
	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, name)
	_, err := c.service.Projects.Locations.Services.Delete(fqName).Do()
	return err
}
