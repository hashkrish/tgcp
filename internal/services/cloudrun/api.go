package cloudrun

import (
	"context"
	"fmt"
	"sort"
	"time"

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

		var revisions []Revision
		latestReady, latestCreated := "", ""
		if item.Status != nil {
			latestReady = item.Status.LatestReadyRevisionName
			latestCreated = item.Status.LatestCreatedRevisionName
			for _, t := range item.Status.Traffic {
				revisions = append(revisions, Revision{
					Name:    t.RevisionName,
					Percent: t.Percent,
					Latest:  t.LatestRevision,
					Tag:     t.Tag,
				})
			}
		}

		services = append(services, RunService{
			Name:                  name,
			Region:                region,
			URL:                   url,
			Status:                status,
			Image:                 image,
			LatestReadyRevision:   latestReady,
			LatestCreatedRevision: latestCreated,
			Revisions:             revisions,
		})
	}
	return services, nil
}

// ListRevisions returns the full revision history for a Cloud Run service
// (unlike the Revisions embedded in RunService from ListServices, which only
// covers revisions currently receiving traffic or holding a URL tag).
// trafficInfo carries the Percent/Latest/Tag data already parsed from the
// service's Status.Traffic; it's merged onto the matching revisions by name.
func (c *Client) ListRevisions(projectID, region, serviceName string, trafficInfo []Revision) ([]Revision, error) {
	if demo.Enabled {
		return loadDemoRevisions(serviceName), nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("cloud run client not initialized")
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	labelSelector := fmt.Sprintf("serving.knative.dev/service=%s", serviceName)

	resp, err := c.service.Projects.Locations.Revisions.List(parent).LabelSelector(labelSelector).Do()
	if err != nil {
		return nil, err
	}

	trafficByName := make(map[string]Revision, len(trafficInfo))
	for _, t := range trafficInfo {
		trafficByName[t.Name] = t
	}

	revisions := make([]Revision, 0, len(resp.Items))
	for _, item := range resp.Items {
		rev := Revision{Name: item.Metadata.Name}
		if item.Spec != nil {
			if containers := item.Spec.Containers; len(containers) > 0 {
				rev.Image = containers[0].Image
			}
		}
		if item.Metadata.CreationTimestamp != "" {
			if created, err := time.Parse(time.RFC3339, item.Metadata.CreationTimestamp); err == nil {
				rev.Created = created
			}
		}
		if t, ok := trafficByName[rev.Name]; ok {
			rev.Percent = t.Percent
			rev.Latest = t.Latest
			rev.Tag = t.Tag
		}
		revisions = append(revisions, rev)
	}

	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Created.After(revisions[j].Created)
	})

	return revisions, nil
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
// full service replace (env vars, resources, concurrency, VPC access,
// ingress, etc.) is explicitly out of scope and skipped. See
// PromoteRevision/TagRevision below for traffic-split management.
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

// PromoteRevision sends 100% of traffic to a single named revision, matching
// `gcloud run services update-traffic --to-revisions=REVISION=100`. This
// replaces the entire traffic split (no partial/N-way splits) -- the
// simplest, most common traffic-split action: rolling back to (or
// re-promoting) a specific revision.
func (c *Client) PromoteRevision(projectID, region, serviceName, revisionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName)

	svc, err := c.service.Projects.Locations.Services.Get(fqName).Do()
	if err != nil {
		return err
	}
	if svc.Spec == nil {
		return fmt.Errorf("service %s has no spec to update", serviceName)
	}
	svc.Spec.Traffic = []*run.TrafficTarget{
		{RevisionName: revisionName, Percent: 100},
	}

	_, err = c.service.Projects.Locations.Services.ReplaceService(fqName, svc).Do()
	return err
}

// TagRevision assigns a URL tag to a revision, matching
// `gcloud run services update-traffic --update-tags=TAG=REVISION`. This
// only adds/updates the tag on the named revision's traffic target --
// every other target's traffic split is preserved untouched.
func (c *Client) TagRevision(projectID, region, serviceName, revisionName, tag string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName)

	svc, err := c.service.Projects.Locations.Services.Get(fqName).Do()
	if err != nil {
		return err
	}
	if svc.Spec == nil {
		return fmt.Errorf("service %s has no spec to update", serviceName)
	}

	found := false
	for _, t := range svc.Spec.Traffic {
		if t.RevisionName == revisionName {
			t.Tag = tag
			found = true
			break
		}
	}
	if !found {
		svc.Spec.Traffic = append(svc.Spec.Traffic, &run.TrafficTarget{
			RevisionName: revisionName,
			Percent:      0,
			Tag:          tag,
		})
	}

	_, err = c.service.Projects.Locations.Services.ReplaceService(fqName, svc).Do()
	return err
}

// SetTrafficSplit replaces the entire traffic split with an arbitrary N-way
// distribution across named revisions, matching `gcloud run services
// update-traffic --to-revisions=REV1=P1,REV2=P2,...`. Any existing URL tags
// on revisions kept in splits are preserved; revisions dropped from splits
// lose their traffic (but keep their tag as a 0%-traffic target, matching
// gcloud's behavior of only touching what --to-revisions names).
func (c *Client) SetTrafficSplit(projectID, region, serviceName string, splits []TrafficSplitEntry) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName)

	svc, err := c.service.Projects.Locations.Services.Get(fqName).Do()
	if err != nil {
		return err
	}
	if svc.Spec == nil {
		return fmt.Errorf("service %s has no spec to update", serviceName)
	}

	existingTags := make(map[string]string, len(svc.Spec.Traffic))
	for _, t := range svc.Spec.Traffic {
		if t.Tag != "" {
			existingTags[t.RevisionName] = t.Tag
		}
	}

	targets := make([]*run.TrafficTarget, 0, len(splits))
	seen := make(map[string]bool, len(splits))
	for _, split := range splits {
		targets = append(targets, &run.TrafficTarget{
			RevisionName: split.RevisionName,
			Percent:      split.Percent,
			Tag:          existingTags[split.RevisionName],
		})
		seen[split.RevisionName] = true
	}
	// Preserve tag-only (0%-traffic) targets for revisions not named in the
	// new split, matching gcloud's --to-revisions semantics.
	for name, tag := range existingTags {
		if !seen[name] {
			targets = append(targets, &run.TrafficTarget{RevisionName: name, Percent: 0, Tag: tag})
		}
	}
	svc.Spec.Traffic = targets

	_, err = c.service.Projects.Locations.Services.ReplaceService(fqName, svc).Do()
	return err
}

// UntagRevision removes a URL tag from a revision, matching
// `gcloud run services update-traffic --remove-tags=TAG`. If the tagged
// revision is carrying 0% traffic (i.e. it only exists as a tagged target,
// not part of the active split), its traffic target is dropped entirely
// rather than left behind with an empty tag.
func (c *Client) UntagRevision(projectID, region, serviceName, revisionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName)

	svc, err := c.service.Projects.Locations.Services.Get(fqName).Do()
	if err != nil {
		return err
	}
	if svc.Spec == nil {
		return fmt.Errorf("service %s has no spec to update", serviceName)
	}

	targets := make([]*run.TrafficTarget, 0, len(svc.Spec.Traffic))
	for _, t := range svc.Spec.Traffic {
		if t.RevisionName == revisionName {
			if t.Percent == 0 {
				continue // drop the tag-only target entirely
			}
			t.Tag = ""
		}
		targets = append(targets, t)
	}
	svc.Spec.Traffic = targets

	_, err = c.service.Projects.Locations.Services.ReplaceService(fqName, svc).Do()
	return err
}

// DeleteRevision deletes a single Cloud Run revision, matching
// `gcloud run revisions delete`. Cloud Run refuses to delete a revision
// still receiving traffic, so no client-side guard is needed here.
func (c *Client) DeleteRevision(projectID, region, revisionName string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("cloud run client not initialized")
	}

	fqName := fmt.Sprintf("projects/%s/locations/%s/revisions/%s", projectID, region, revisionName)
	_, err := c.service.Projects.Locations.Revisions.Delete(fqName).Do()
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
