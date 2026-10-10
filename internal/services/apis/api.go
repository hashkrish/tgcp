package apis

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/serviceusage/v1"
)

// opPollInterval / opTimeout bound how long Enable/Disable wait on the
// long-running operation Service Usage returns. Enabling an API typically
// completes in a few seconds, but some (e.g. container.googleapis.com) take
// close to a minute while they provision service agents.
const (
	opPollInterval = 2 * time.Second
	opTimeout      = 3 * time.Minute
)

type Client struct {
	service *serviceusage.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	httpClient, err := core.NewHTTPClient(ctx, serviceusage.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create http client: %w", err)
	}
	svc, err := serviceusage.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("failed to create service usage client: %w", err)
	}
	return &Client{service: svc}, nil
}

// commonAPIs is the curated set shown on the Common tab: every API a tgcp
// service talks to, plus other frequently-enabled Google Cloud APIs. The
// full catalog (`gcloud services list --available`) is ~13k entries over
// ~65 pages -- almost all Marketplace partner listings -- which takes
// minutes to page through and trips Service Usage's rate limit (429), so
// it's deliberately not offered. Anything not listed here can still be
// enabled by name.
var commonAPIs = []string{
	"compute", "container", "run", "appengine", "cloudfunctions",
	"storage", "file",
	"sqladmin", "spanner", "bigtableadmin", "redis", "firestore", "datastore",
	"bigquery", "dataflow", "dataproc", "pubsub", "cloudscheduler", "cloudtasks",
	"iam", "iamcredentials", "secretmanager", "parametermanager", "cloudkms",
	"dns", "vpcaccess", "servicenetworking", "networkmanagement", "certificatemanager",
	"logging", "monitoring", "cloudtrace", "clouderrorreporting",
	"cloudbuild", "artifactregistry", "containeranalysis", "containerscanning",
	"serviceusage", "cloudresourcemanager", "cloudbilling", "billingbudgets",
	"recommender", "cloudasset", "osconfig", "oslogin",
	"aiplatform", "eventarc", "workflows", "composer", "apigateway",
}

// batchGetMax is the most names services:batchGet accepts per call.
const batchGetMax = 30

// ListEnabledAPIs lists the project's enabled APIs, matching
// `gcloud services list`.
func (c *Client) ListEnabledAPIs(projectID string) ([]API, error) {
	if demo.Enabled {
		return loadDemoAPIs(true), nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	call := c.service.Services.List("projects/" + projectID).PageSize(200).Filter("state:ENABLED")

	var out []API
	err := call.Pages(context.Background(), func(resp *serviceusage.ListServicesResponse) error {
		for _, svc := range resp.Services {
			out = append(out, toAPI(svc))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortAPIs(out)
	return out, nil
}

// ListCommonAPIs returns the enabled/disabled state of every API in
// commonAPIs, fetched with services:batchGet (two calls for the current
// list) instead of paging the full catalog.
func (c *Client) ListCommonAPIs(projectID string) ([]API, error) {
	if demo.Enabled {
		return loadDemoAPIs(false), nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("client not initialized")
	}

	parent := "projects/" + projectID
	var out []API
	for start := 0; start < len(commonAPIs); start += batchGetMax {
		end := min(start+batchGetMax, len(commonAPIs))
		names := make([]string, 0, end-start)
		for _, n := range commonAPIs[start:end] {
			names = append(names, serviceResource(projectID, n+".googleapis.com"))
		}
		resp, err := c.service.Services.BatchGet(parent).Names(names...).Do()
		if err != nil {
			return nil, err
		}
		for _, svc := range resp.Services {
			out = append(out, toAPI(svc))
		}
	}
	sortAPIs(out)
	return out, nil
}

func toAPI(svc *serviceusage.GoogleApiServiceusageV1Service) API {
	a := API{
		Name:  shortName(svc.Name),
		State: svc.State,
	}
	if cfg := svc.Config; cfg != nil {
		if cfg.Name != "" {
			a.Name = cfg.Name
		}
		a.Title = cfg.Title
		if cfg.Documentation != nil {
			a.Summary = strings.TrimSpace(cfg.Documentation.Summary)
		}
	}
	return a
}

// sortAPIs orders by title (falling back to name), which is how the Console
// lists them and makes scanning a long list easier than raw service names.
func sortAPIs(items []API) {
	sort.SliceStable(items, func(i, j int) bool {
		return strings.ToLower(displayTitle(items[i])) < strings.ToLower(displayTitle(items[j]))
	})
}

func displayTitle(a API) string {
	if a.Title != "" {
		return a.Title
	}
	return a.Name
}

// shortName turns "projects/123/services/compute.googleapis.com" into
// "compute.googleapis.com".
func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}

// EnableAPI enables an API on the project, matching
// `gcloud services enable <name>`, and waits for the operation to finish.
func (c *Client) EnableAPI(projectID, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("client not initialized")
	}
	op, err := c.service.Services.Enable(serviceResource(projectID, name), &serviceusage.EnableServiceRequest{}).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(op)
}

// DisableAPI disables an API on the project, matching
// `gcloud services disable <name>` (or `--force` when disableDependents is
// set). Without disableDependents, the API refuses to disable a service
// other enabled services depend on, and that error is surfaced as-is.
func (c *Client) DisableAPI(projectID, name string, disableDependents bool) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("client not initialized")
	}
	req := &serviceusage.DisableServiceRequest{DisableDependentServices: disableDependents}
	op, err := c.service.Services.Disable(serviceResource(projectID, name), req).Do()
	if err != nil {
		return err
	}
	return c.waitOperation(op)
}

func serviceResource(projectID, name string) string {
	return fmt.Sprintf("projects/%s/services/%s", projectID, name)
}

// waitOperation polls a Service Usage long-running operation until it
// completes, returning its error (if any). Runs inside a tea.Cmd goroutine,
// so blocking here doesn't stall the UI.
func (c *Client) waitOperation(op *serviceusage.Operation) error {
	deadline := time.Now().Add(opTimeout)
	for {
		if op.Done {
			if op.Error != nil {
				return fmt.Errorf("%s (code %d)", op.Error.Message, op.Error.Code)
			}
			return nil
		}
		if op.Name == "" {
			// Some calls complete synchronously and return no operation name
			// to poll; treat that as done.
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for operation %s; it may still complete in the background", op.Name)
		}
		time.Sleep(opPollInterval)
		next, err := c.service.Operations.Get(op.Name).Do()
		if err != nil {
			return fmt.Errorf("poll operation %s: %w", op.Name, err)
		}
		op = next
	}
}
