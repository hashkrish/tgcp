package scheduler

import (
	"context"
	"fmt"
	"strings"

	gscheduler "cloud.google.com/go/scheduler/apiv1"
	"cloud.google.com/go/scheduler/apiv1/schedulerpb"
	"github.com/yogirk/tgcp/internal/demo"
	locationpb "google.golang.org/genproto/googleapis/cloud/location"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type Client struct {
	client *gscheduler.CloudSchedulerClient
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := gscheduler.NewCloudSchedulerClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("scheduler client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListJobs lists Cloud Scheduler jobs across every region the project has
// jobs in. Cloud Scheduler jobs are region-scoped (ListJobs requires a
// parent of the form projects/{project}/locations/{location}), so we first
// discover the project's available locations via the Cloud Locations API,
// then fan out one ListJobs call per location. This keeps things correct
// without hardcoding a region list, and each location that has no jobs
// simply returns an empty page (no N+1 growth relative to job count).
func (c *Client) ListJobs(projectID string) ([]Job, error) {
	if demo.Enabled {
		return []Job{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()

	locations, err := c.listLocationIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var jobs []Job
	for _, loc := range locations {
		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
		it := c.client.ListJobs(ctx, &schedulerpb.ListJobsRequest{Parent: parent})
		for j, err := range it.All() {
			if err != nil {
				return nil, fmt.Errorf("list jobs in %s: %w", loc, err)
			}
			jobs = append(jobs, toJob(j, loc))
		}
	}
	return jobs, nil
}

// listLocationIDs returns the canonical location IDs (e.g. "us-central1")
// available to Cloud Scheduler for this project.
func (c *Client) listLocationIDs(ctx context.Context, projectID string) ([]string, error) {
	var ids []string
	req := &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}
	it := c.client.ListLocations(ctx, req)
	for loc, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list scheduler locations: %w", err)
		}
		ids = append(ids, loc.LocationId)
	}
	return ids, nil
}

func toJob(j *schedulerpb.Job, location string) Job {
	targetType, targetSummary := describeTarget(j)

	retryCount := int32(0)
	maxRetryDur := ""
	if rc := j.GetRetryConfig(); rc != nil {
		retryCount = rc.GetRetryCount()
		if d := rc.GetMaxRetryDuration(); d != nil {
			maxRetryDur = d.AsDuration().String()
		}
	}

	scheduleTime := ""
	if t := j.GetScheduleTime(); t != nil {
		scheduleTime = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}
	lastAttempt := ""
	if t := j.GetLastAttemptTime(); t != nil {
		lastAttempt = t.AsTime().Local().Format("2006-01-02 15:04:05 MST")
	}

	return Job{
		Name:          shortName(j.GetName()),
		Location:      location,
		Schedule:      j.GetSchedule(),
		TimeZone:      j.GetTimeZone(),
		State:         j.GetState().String(),
		TargetType:    targetType,
		TargetSummary: targetSummary,
		ScheduleTime:  scheduleTime,
		LastAttempt:   lastAttempt,
		RetryCount:    retryCount,
		MaxRetryDur:   maxRetryDur,
	}
}

// describeTarget returns a human-readable target type and destination
// summary for a job, regardless of which target kind it uses.
func describeTarget(j *schedulerpb.Job) (targetType, summary string) {
	switch {
	case j.GetHttpTarget() != nil:
		return "HTTP", j.GetHttpTarget().GetUri()
	case j.GetPubsubTarget() != nil:
		return "Pub/Sub", j.GetPubsubTarget().GetTopicName()
	case j.GetAppEngineHttpTarget() != nil:
		return "App Engine", j.GetAppEngineHttpTarget().GetRelativeUri()
	default:
		return "Unknown", ""
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}

// CreateHTTPJob creates a new Cloud Scheduler job with an HTTP target. This
// MVP only supports HTTP targets — Pub/Sub and App Engine targets need
// extra nested fields (topic/message or service/version routing) that
// don't have a sensible minimal-form representation, so they're
// deliberately out of scope here.
func (c *Client) CreateHTTPJob(projectID, location, name, schedule, uri string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, location)
	jobName := fmt.Sprintf("%s/jobs/%s", parent, name)
	job := &schedulerpb.Job{
		Name:     jobName,
		Schedule: schedule,
		Target: &schedulerpb.Job_HttpTarget{
			HttpTarget: &schedulerpb.HttpTarget{
				Uri:        uri,
				HttpMethod: schedulerpb.HttpMethod_POST,
			},
		},
	}
	_, err := c.client.CreateJob(context.Background(), &schedulerpb.CreateJobRequest{
		Parent: parent,
		Job:    job,
	})
	return err
}

// DeleteJob deletes a Cloud Scheduler job, matching
// `gcloud scheduler jobs delete`.
func (c *Client) DeleteJob(projectID, location, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	jobName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, location, name)
	return c.client.DeleteJob(context.Background(), &schedulerpb.DeleteJobRequest{Name: jobName})
}

// PauseJob pauses a job, matching `gcloud scheduler jobs pause`. A paused
// job's schedule stops firing until resumed.
func (c *Client) PauseJob(projectID, location, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	jobName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, location, name)
	_, err := c.client.PauseJob(context.Background(), &schedulerpb.PauseJobRequest{Name: jobName})
	return err
}

// ResumeJob resumes a paused job, matching `gcloud scheduler jobs resume`.
func (c *Client) ResumeJob(projectID, location, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	jobName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, location, name)
	_, err := c.client.ResumeJob(context.Background(), &schedulerpb.ResumeJobRequest{Name: jobName})
	return err
}

// RunJob triggers an on-demand execution of a job right now, matching
// `gcloud scheduler jobs run`.
func (c *Client) RunJob(projectID, location, name string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	jobName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, location, name)
	_, err := c.client.RunJob(context.Background(), &schedulerpb.RunJobRequest{Name: jobName})
	return err
}

// UpdateJobSchedule patches only the cron schedule of an existing job via
// UpdateMask, leaving the target and every other field untouched.
func (c *Client) UpdateJobSchedule(projectID, location, name, schedule string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("scheduler client not initialized")
	}
	jobName := fmt.Sprintf("projects/%s/locations/%s/jobs/%s", projectID, location, name)
	job := &schedulerpb.Job{
		Name:     jobName,
		Schedule: schedule,
	}
	_, err := c.client.UpdateJob(context.Background(), &schedulerpb.UpdateJobRequest{
		Job:        job,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"schedule"}},
	})
	return err
}
