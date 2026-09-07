package appengine

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	appengine "google.golang.org/api/appengine/v1"
	"google.golang.org/api/option"
)

// Client wraps the App Engine Admin API (v1 REST). Every mutating call in
// this API (Services.Patch/Delete, Versions.Patch/Delete) returns a
// long-running *appengine.Operation rather than completing synchronously;
// this client deliberately does not poll operations to completion (no other
// service in this codebase does either) -- callers fire the mutation, show
// an immediate "Stopping v1..."-style toast, and rely on a later refresh to
// reflect the eventual state.
type Client struct {
	service   *appengine.APIService
	projectID string
}

// NewClient initializes a new App Engine Admin API client.
func NewClient(ctx context.Context, projectID string) (*Client, error) {
	if demo.Enabled {
		return &Client{projectID: projectID}, nil
	}
	svc, err := appengine.NewService(ctx, option.WithScopes(appengine.CloudPlatformScope))
	if err != nil {
		return nil, fmt.Errorf("failed to create app engine service: %w", err)
	}
	return &Client{service: svc, projectID: projectID}, nil
}

// GetApplication fetches the project's single App Engine application.
// Returns an error (typically a 404) if App Engine isn't enabled on the
// project -- there is no Apps.List; a project has at most one Application,
// addressed directly by project ID.
func (c *Client) GetApplication(ctx context.Context) (*Application, error) {
	if demo.Enabled {
		return &Application{Id: c.projectID, LocationId: "us-central", ServingStatus: "SERVING"}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("app engine client not initialized")
	}
	app, err := c.service.Apps.Get(c.projectID).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	return &Application{
		Id:              app.Id,
		DefaultHostname: app.DefaultHostname,
		LocationId:      app.LocationId,
		ServingStatus:   app.ServingStatus,
	}, nil
}

// ListServices lists every service in the project's App Engine application,
// matching `gcloud app services list`.
func (c *Client) ListServices(ctx context.Context) ([]AppEngineService, error) {
	if demo.Enabled {
		return []AppEngineService{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("app engine client not initialized")
	}
	var out []AppEngineService
	err := c.service.Apps.Services.List(c.projectID).Pages(ctx, func(resp *appengine.ListServicesResponse) error {
		for _, svc := range resp.Services {
			allocations := map[string]float64{}
			if svc.Split != nil {
				allocations = svc.Split.Allocations
			}
			out = append(out, AppEngineService{Id: svc.Id, Split: allocations})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Id < out[j].Id })
	return out, nil
}

// SetTrafficSplit replaces a service's entire traffic split, matching
// `gcloud app services set-traffic --splits=v1=.5,v2=.5`.
func (c *Client) SetTrafficSplit(ctx context.Context, serviceID string, allocations map[string]float64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("app engine client not initialized")
	}
	_, err := c.service.Apps.Services.Patch(c.projectID, serviceID, &appengine.Service{
		Split: &appengine.TrafficSplit{Allocations: allocations},
	}).UpdateMask("split").Context(ctx).Do()
	return err
}

// DeleteService deletes a service and all its versions, matching
// `gcloud app services delete`.
func (c *Client) DeleteService(ctx context.Context, serviceID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("app engine client not initialized")
	}
	_, err := c.service.Apps.Services.Delete(c.projectID, serviceID).Context(ctx).Do()
	return err
}

// ListVersions lists every version of a service, matching
// `gcloud app versions list`. split is the owning service's current
// traffic split (from ListServices), used to populate each Version's
// Traffic fraction.
func (c *Client) ListVersions(ctx context.Context, serviceID string, split map[string]float64) ([]Version, error) {
	if demo.Enabled {
		return []Version{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("app engine client not initialized")
	}
	var out []Version
	err := c.service.Apps.Services.Versions.List(c.projectID, serviceID).View("FULL").Pages(ctx, func(resp *appengine.ListVersionsResponse) error {
		for _, v := range resp.Versions {
			created, _ := time.Parse(time.RFC3339, v.CreateTime)
			out = append(out, Version{
				Id:            v.Id,
				ServingStatus: v.ServingStatus,
				Runtime:       v.Runtime,
				Env:           v.Env,
				InstanceClass: v.InstanceClass,
				CreateTime:    created,
				VersionUrl:    v.VersionUrl,
				Threadsafe:    v.Threadsafe,
				Traffic:       split[v.Id],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreateTime.After(out[j].CreateTime) })
	return out, nil
}

// SetVersionServingStatus starts ("SERVING") or stops ("STOPPED") a
// version, matching `gcloud app versions start`/`stop`.
func (c *Client) SetVersionServingStatus(ctx context.Context, serviceID, versionID, status string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("app engine client not initialized")
	}
	_, err := c.service.Apps.Services.Versions.Patch(c.projectID, serviceID, versionID, &appengine.Version{
		ServingStatus: status,
	}).UpdateMask("servingStatus").Context(ctx).Do()
	return err
}

// DeleteVersion deletes a version, matching `gcloud app versions delete`.
func (c *Client) DeleteVersion(ctx context.Context, serviceID, versionID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("app engine client not initialized")
	}
	_, err := c.service.Apps.Services.Versions.Delete(c.projectID, serviceID, versionID).Context(ctx).Do()
	return err
}

// ListInstances lists the running instances of a version, matching
// `gcloud app instances list`.
func (c *Client) ListInstances(ctx context.Context, serviceID, versionID string) ([]Instance, error) {
	if demo.Enabled {
		return []Instance{}, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("app engine client not initialized")
	}
	var out []Instance
	err := c.service.Apps.Services.Versions.Instances.List(c.projectID, serviceID, versionID).Pages(ctx, func(resp *appengine.ListInstancesResponse) error {
		for _, inst := range resp.Instances {
			start, _ := time.Parse(time.RFC3339, inst.StartTime)
			out = append(out, Instance{
				Id:           inst.Id,
				VmStatus:     inst.VmStatus,
				Availability: inst.Availability,
				StartTime:    start,
				Requests:     inst.Requests,
				Errors:       inst.Errors,
				Qps:          inst.Qps,
			})
		}
		return nil
	})
	return out, err
}
