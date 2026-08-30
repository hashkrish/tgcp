package dataflow

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	dataflow "google.golang.org/api/dataflow/v1b3"
)

type Client struct {
	service *dataflow.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := dataflow.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("dataflow client: %w", err)
	}
	return &Client{service: svc}, nil
}

// LaunchTemplateJob launches a new Dataflow job from a classic GCS template.
func (c *Client) LaunchTemplateJob(projectID, region, jobName, gcsPath string, parameters map[string]string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataflow client not initialized")
	}
	params := &dataflow.LaunchTemplateParameters{
		JobName:    jobName,
		Parameters: parameters,
	}
	_, err := c.service.Projects.Templates.Launch(projectID, params).
		GcsPath(gcsPath).
		Location(region).
		Do()
	return err
}

// ArchiveJob archives a Dataflow job, matching `gcloud dataflow jobs
// archive`. Dataflow has no true delete for jobs — archiving sets the
// "archived" label on the job via a labels-only Update, which is what the
// gcloud CLI's archive command does under the hood. Only jobs already in a
// terminal state (DONE/CANCELLED/DRAINED/FAILED/UPDATED) can be archived;
// the API itself rejects archiving a still-running job.
func (c *Client) ArchiveJob(projectID, region, jobID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataflow client not initialized")
	}
	job := &dataflow.Job{
		Labels: map[string]string{"archived": "true"},
	}
	_, err := c.service.Projects.Jobs.Update(projectID, jobID, job).
		Location(region).
		UpdateMask("labels").
		Do()
	return err
}

// CancelJob requests immediate cancellation of a running Dataflow job,
// matching `gcloud dataflow jobs cancel`. Cancellation stops processing
// right away and does not flush in-flight streaming data.
func (c *Client) CancelJob(projectID, region, jobID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataflow client not initialized")
	}
	job := &dataflow.Job{
		RequestedState: "JOB_STATE_CANCELLED",
	}
	_, err := c.service.Projects.Jobs.Update(projectID, jobID, job).
		Location(region).
		Do()
	return err
}

// DrainJob requests a graceful drain of a running streaming Dataflow job,
// matching `gcloud dataflow jobs drain`. Draining stops ingesting new data
// but lets in-flight data finish processing before the job stops; it only
// applies to streaming jobs.
func (c *Client) DrainJob(projectID, region, jobID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataflow client not initialized")
	}
	job := &dataflow.Job{
		RequestedState: "JOB_STATE_DRAINING",
	}
	_, err := c.service.Projects.Jobs.Update(projectID, jobID, job).
		Location(region).
		Do()
	return err
}

// UpdateJobOptions updates a running Streaming Engine job's autoscaling
// bounds, matching `gcloud dataflow jobs update-options
// --min-num-workers --max-num-workers`. minWorkers/maxWorkers of 0 leaves
// that bound unchanged (the API only applies fields present in the update
// mask). Other RuntimeUpdatableParams (latency tier, worker utilization
// hint) are out of scope for this minimal update flow.
func (c *Client) UpdateJobOptions(projectID, region, jobID string, minWorkers, maxWorkers int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("dataflow client not initialized")
	}
	params := &dataflow.RuntimeUpdatableParams{}
	var maskFields []string
	if minWorkers > 0 {
		params.MinNumWorkers = minWorkers
		maskFields = append(maskFields, "runtime_updatable_params.min_num_workers")
	}
	if maxWorkers > 0 {
		params.MaxNumWorkers = maxWorkers
		maskFields = append(maskFields, "runtime_updatable_params.max_num_workers")
	}
	job := &dataflow.Job{RuntimeUpdatableParams: params}
	_, err := c.service.Projects.Jobs.Update(projectID, jobID, job).
		Location(region).
		UpdateMask(strings.Join(maskFields, ",")).
		Do()
	return err
}

func (c *Client) ListJobs(projectID string) ([]Job, error) {
	if demo.Enabled {
		return []Job{}, nil
	}
	var jobs []Job
	// Dataflow is regional, but has an aggregated list "jobs.aggregated" in v1b3?
	// Actually projects.jobs.aggregatedList exists.

	call := c.service.Projects.Jobs.Aggregated(projectID)
	err := call.Pages(context.Background(), func(page *dataflow.ListJobsResponse) error {
		for _, j := range page.Jobs {
			// Clean up state string "JOB_STATE_RUNNING" -> "RUNNING"
			// Clean up type "JOB_TYPE_STREAMING" -> "STREAMING"

			jobs = append(jobs, Job{
				ID:               j.Id,
				Name:             j.Name,
				Type:             j.Type,
				State:            j.CurrentState,
				CreateTime:       j.CreateTime,
				Location:         j.Location,
				StartTime:        j.StartTime,
				CurrentStateTime: j.CurrentStateTime,
				ReplacedByJobID:  j.ReplacedByJobId,
			})
		}
		return nil
	})
	return jobs, err
}
