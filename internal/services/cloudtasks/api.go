package cloudtasks

import (
	"context"
	"fmt"
	"strings"

	gtasks "cloud.google.com/go/cloudtasks/apiv2"
	"cloud.google.com/go/cloudtasks/apiv2/cloudtaskspb"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	"github.com/yogirk/tgcp/internal/demo"

	locationpb "google.golang.org/genproto/googleapis/cloud/location"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type Client struct {
	client *gtasks.Client
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	c, err := gtasks.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloudtasks client: %w", err)
	}
	return &Client{client: c}, nil
}

// ListQueues lists Cloud Tasks queues across every region the project has
// queues in. Like Cloud Scheduler jobs, Cloud Tasks queues are region-scoped
// (ListQueues requires a parent of the form
// projects/{project}/locations/{location}) and there's no project-wide
// "list all queues everywhere" call. Rather than hardcoding a fixed list of
// regions (which would silently miss queues in newer/less common regions),
// we first discover the project's available locations via the Cloud
// Locations API (GetLocation/ListLocations, exposed by this client), then
// fan out one ListQueues call per location. Locations with no queues just
// return an empty page, so this stays a small, bounded number of calls
// (one per region) rather than growing with the number of queues.
func (c *Client) ListQueues(projectID string) ([]Queue, error) {
	if demo.Enabled {
		return []Queue{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("client not init")
	}
	ctx := context.Background()

	locations, err := c.listLocationIDs(ctx, projectID)
	if err != nil {
		return nil, err
	}

	var queues []Queue
	for _, loc := range locations {
		parent := fmt.Sprintf("projects/%s/locations/%s", projectID, loc)
		it := c.client.ListQueues(ctx, &cloudtaskspb.ListQueuesRequest{Parent: parent})
		for q, err := range it.All() {
			if err != nil {
				return nil, fmt.Errorf("list queues in %s: %w", loc, err)
			}
			queues = append(queues, toQueue(q, loc))
		}
	}
	return queues, nil
}

// listLocationIDs returns the canonical location IDs (e.g. "us-central1")
// available to Cloud Tasks for this project.
func (c *Client) listLocationIDs(ctx context.Context, projectID string) ([]string, error) {
	var ids []string
	req := &locationpb.ListLocationsRequest{
		Name: fmt.Sprintf("projects/%s", projectID),
	}
	it := c.client.ListLocations(ctx, req)
	for loc, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list cloudtasks locations: %w", err)
		}
		ids = append(ids, loc.LocationId)
	}
	return ids, nil
}

func toQueue(q *cloudtaskspb.Queue, location string) Queue {
	var maxDispatchRate float64
	var maxConcurrent int32
	if rl := q.GetRateLimits(); rl != nil {
		maxDispatchRate = rl.GetMaxDispatchesPerSecond()
		maxConcurrent = rl.GetMaxConcurrentDispatches()
	}

	var maxAttempts int32
	maxRetryDuration := ""
	if rc := q.GetRetryConfig(); rc != nil {
		maxAttempts = rc.GetMaxAttempts()
		if d := rc.GetMaxRetryDuration(); d != nil {
			maxRetryDuration = d.AsDuration().String()
		}
	}

	return Queue{
		Name:             shortName(q.GetName()),
		Location:         location,
		State:            q.GetState().String(),
		MaxDispatchRate:  maxDispatchRate,
		MaxConcurrent:    maxConcurrent,
		MaxAttempts:      maxAttempts,
		MaxRetryDuration: maxRetryDuration,
	}
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}

// CreateQueue creates a new Cloud Tasks queue with the given short queue ID
// in the given location.
func (c *Client) CreateQueue(projectID, location, queueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, location)
	name := fmt.Sprintf("%s/queues/%s", parent, queueID)
	_, err := c.client.CreateQueue(context.Background(), &cloudtaskspb.CreateQueueRequest{
		Parent: parent,
		Queue:  &cloudtaskspb.Queue{Name: name},
	})
	return err
}

// DeleteQueue deletes a Cloud Tasks queue, matching
// `gcloud tasks queues delete`.
func (c *Client) DeleteQueue(projectID, location, queueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	return c.client.DeleteQueue(context.Background(), &cloudtaskspb.DeleteQueueRequest{Name: name})
}

// PauseQueue pauses a queue, matching `gcloud tasks queues pause`. A paused
// queue stops dispatching tasks but continues to accept new ones.
func (c *Client) PauseQueue(projectID, location, queueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	_, err := c.client.PauseQueue(context.Background(), &cloudtaskspb.PauseQueueRequest{Name: name})
	return err
}

// ResumeQueue resumes a paused (or disabled) queue, matching
// `gcloud tasks queues resume`.
func (c *Client) ResumeQueue(projectID, location, queueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	_, err := c.client.ResumeQueue(context.Background(), &cloudtaskspb.ResumeQueueRequest{Name: name})
	return err
}

// PurgeQueue deletes every task currently queued in a queue without
// deleting the queue itself, matching `gcloud tasks queues purge`.
func (c *Client) PurgeQueue(projectID, location, queueID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	_, err := c.client.PurgeQueue(context.Background(), &cloudtaskspb.PurgeQueueRequest{Name: name})
	return err
}

// GetQueueIAMPolicy reads a queue's current IAM policy, matching
// `gcloud tasks queues get-iam-policy`.
func (c *Client) GetQueueIAMPolicy(projectID, location, queueID string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	policy, err := c.client.GetIamPolicy(context.Background(), &iampb.GetIamPolicyRequest{Resource: name})
	if err != nil {
		return nil, fmt.Errorf("get queue IAM policy: %w", err)
	}
	out := make([]IAMBinding, 0, len(policy.Bindings))
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddQueueIAMBinding grants a role to a member on a queue, matching
// `gcloud tasks queues add-iam-policy-binding`. It fetches the current
// policy, merges the new binding into it, and writes the whole policy back —
// this never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddQueueIAMBinding(projectID, location, queueID, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	ctx := context.Background()
	policy, err := c.client.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{Resource: name})
	if err != nil {
		return fmt.Errorf("get queue IAM policy: %w", err)
	}

	found := false
	for _, b := range policy.Bindings {
		if b.Role != role {
			continue
		}
		found = true
		for _, m := range b.Members {
			if m == member {
				return nil // already granted
			}
		}
		b.Members = append(b.Members, member)
		break
	}
	if !found {
		policy.Bindings = append(policy.Bindings, &iampb.Binding{Role: role, Members: []string{member}})
	}

	_, err = c.client.SetIamPolicy(ctx, &iampb.SetIamPolicyRequest{Resource: name, Policy: policy})
	return err
}

// ListTasks lists the tasks currently queued in a queue, matching
// `gcloud tasks list --queue`.
func (c *Client) ListTasks(projectID, location, queueID string) ([]Task, error) {
	if demo.Enabled {
		return []Task{}, nil
	}
	if c.client == nil {
		return nil, fmt.Errorf("cloudtasks client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	var tasks []Task
	it := c.client.ListTasks(context.Background(), &cloudtaskspb.ListTasksRequest{Parent: parent})
	for t, err := range it.All() {
		if err != nil {
			return nil, fmt.Errorf("list tasks: %w", err)
		}
		tasks = append(tasks, toTask(t))
	}
	return tasks, nil
}

func toTask(t *cloudtaskspb.Task) Task {
	task := Task{Name: shortName(t.GetName())}
	if req := t.GetHttpRequest(); req != nil {
		task.URL = req.GetUrl()
		task.HTTPMethod = req.GetHttpMethod().String()
	}
	if ts := t.GetScheduleTime(); ts != nil {
		task.ScheduleTime = ts.AsTime().Format("2006-01-02 15:04:05")
	}
	if ts := t.GetCreateTime(); ts != nil {
		task.CreateTime = ts.AsTime().Format("2006-01-02 15:04:05")
	}
	task.DispatchCnt = t.GetDispatchCount()
	task.ResponseCnt = t.GetResponseCount()
	return task
}

// CreateHTTPTask creates a new task with an HTTP target on the given queue,
// matching `gcloud tasks create-http-task`. App Engine-target tasks are
// deliberately out of scope.
func (c *Client) CreateHTTPTask(projectID, location, queueID, url, method string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	httpMethod := cloudtaskspb.HttpMethod_POST
	if m, ok := cloudtaskspb.HttpMethod_value[strings.ToUpper(method)]; ok {
		httpMethod = cloudtaskspb.HttpMethod(m)
	}
	_, err := c.client.CreateTask(context.Background(), &cloudtaskspb.CreateTaskRequest{
		Parent: parent,
		Task: &cloudtaskspb.Task{
			MessageType: &cloudtaskspb.Task_HttpRequest{
				HttpRequest: &cloudtaskspb.HttpRequest{
					Url:        url,
					HttpMethod: httpMethod,
				},
			},
		},
	})
	return err
}

// RunTask forces a scheduled task to run now, matching `gcloud tasks run`.
func (c *Client) RunTask(projectID, location, queueID, taskID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s", projectID, location, queueID, taskID)
	_, err := c.client.RunTask(context.Background(), &cloudtaskspb.RunTaskRequest{Name: name})
	return err
}

// DeleteTask deletes a single task, matching `gcloud tasks delete`.
func (c *Client) DeleteTask(projectID, location, queueID, taskID string) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s/tasks/%s", projectID, location, queueID, taskID)
	return c.client.DeleteTask(context.Background(), &cloudtaskspb.DeleteTaskRequest{Name: name})
}

// UpdateQueueMaxDispatchRate patches a queue's max dispatches-per-second
// rate limit, matching `gcloud tasks queues update --max-dispatches-per-second`.
// Max concurrent dispatches, retry config, and app-engine routing overrides
// are out of scope for this minimal Update flow.
func (c *Client) UpdateQueueMaxDispatchRate(projectID, location, queueID string, maxDispatchRate float64) error {
	if demo.Enabled {
		return nil
	}
	if c.client == nil {
		return fmt.Errorf("cloudtasks client not initialized")
	}
	name := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	_, err := c.client.UpdateQueue(context.Background(), &cloudtaskspb.UpdateQueueRequest{
		Queue: &cloudtaskspb.Queue{
			Name: name,
			RateLimits: &cloudtaskspb.RateLimits{
				MaxDispatchesPerSecond: maxDispatchRate,
			},
		},
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"rate_limits.max_dispatches_per_second"}},
	})
	return err
}
