package pubsub

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/pubsub/v1"
)

type Client struct {
	service *pubsub.Service
}

func NewClient(ctx context.Context) (*Client, error) {
	if demo.Enabled {
		return &Client{}, nil
	}
	svc, err := pubsub.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("pubsub client: %w", err)
	}
	return &Client{service: svc}, nil
}

func (c *Client) ListTopics(projectID string) ([]Topic, error) {
	if demo.Enabled {
		return []Topic{}, nil
	}
	var topics []Topic
	parent := fmt.Sprintf("projects/%s", projectID)

	err := c.service.Projects.Topics.List(parent).Pages(context.Background(), func(page *pubsub.ListTopicsResponse) error {
		for _, t := range page.Topics {
			topics = append(topics, Topic{
				Name:       shortName(t.Name),
				ProjectID:  projectID,
				Labels:     t.Labels,
				KmsKeyName: t.KmsKeyName,
			})
		}
		return nil
	})
	return topics, err
}

func (c *Client) ListSubscriptions(projectID string) ([]Subscription, error) {
	if demo.Enabled {
		return []Subscription{}, nil
	}
	var subs []Subscription
	parent := fmt.Sprintf("projects/%s", projectID)

	err := c.service.Projects.Subscriptions.List(parent).Pages(context.Background(), func(page *pubsub.ListSubscriptionsResponse) error {
		for _, s := range page.Subscriptions {
			dlTopic := ""
			if s.DeadLetterPolicy != nil {
				dlTopic = shortName(s.DeadLetterPolicy.DeadLetterTopic)
			}

			pushEp := ""
			if s.PushConfig != nil {
				pushEp = s.PushConfig.PushEndpoint
			}

			subs = append(subs, Subscription{
				Name:              shortName(s.Name),
				Topic:             shortName(s.Topic),
				PushEndpoint:      pushEp,
				AckDeadline:       int(s.AckDeadlineSeconds),
				RetainAcked:       s.RetainAckedMessages,
				RetentionDuration: s.MessageRetentionDuration,
				DeadLetterTopic:   dlTopic,
				State:             s.State,
			})
		}
		return nil
	})
	return subs, err
}

func shortName(longName string) string {
	parts := strings.Split(longName, "/")
	return parts[len(parts)-1]
}

// CreateTopic creates a new Pub/Sub topic with the given short topic ID.
func (c *Client) CreateTopic(projectID, topicID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	_, err := c.service.Projects.Topics.Create(name, &pubsub.Topic{}).Do()
	return err
}

// CreateSubscription creates a new pull subscription bound to an existing
// topic (topicID is a short topic name in the same project).
func (c *Client) CreateSubscription(projectID, subID, topicID string, ackDeadline int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	topic := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	sub := &pubsub.Subscription{
		Topic: topic,
	}
	if ackDeadline > 0 {
		sub.AckDeadlineSeconds = int64(ackDeadline)
	}
	_, err := c.service.Projects.Subscriptions.Create(name, sub).Do()
	return err
}

// DeleteTopic deletes a Pub/Sub topic, matching `gcloud pubsub topics
// delete`. Existing subscriptions to the topic are left in place but become
// detached (they still deliver any already-queued messages).
func (c *Client) DeleteTopic(projectID, topicID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	_, err := c.service.Projects.Topics.Delete(name).Do()
	return err
}

// DeleteSubscription deletes a Pub/Sub subscription, matching
// `gcloud pubsub subscriptions delete`. Any messages not yet acked are lost.
func (c *Client) DeleteSubscription(projectID, subID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	_, err := c.service.Projects.Subscriptions.Delete(name).Do()
	return err
}

// DetachSubscription detaches a subscription from its topic, matching
// `gcloud pubsub topics detach-subscription`. The subscription keeps
// delivering any already-queued messages but never receives new ones.
func (c *Client) DetachSubscription(projectID, subID string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	_, err := c.service.Projects.Subscriptions.Detach(name).Do()
	return err
}

// GetTopicIAMPolicy reads a topic's current IAM policy, matching
// `gcloud pubsub topics get-iam-policy`. Used as the "look before you grant"
// read step before AddTopicIAMBinding.
func (c *Client) GetTopicIAMPolicy(projectID, topicID string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	policy, err := c.service.Projects.Topics.GetIamPolicy(name).Do()
	if err != nil {
		return nil, fmt.Errorf("get topic IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddTopicIAMBinding grants a role to a member on a topic, matching
// `gcloud pubsub topics add-iam-policy-binding`. It fetches the current
// policy, merges the new binding into it (appending to an existing role's
// members, or adding a new role entry), and writes the whole policy back —
// this never drops any existing binding, unlike a raw set-iam-policy.
func (c *Client) AddTopicIAMBinding(projectID, topicID, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	policy, err := c.service.Projects.Topics.GetIamPolicy(name).Do()
	if err != nil {
		return fmt.Errorf("get topic IAM policy: %w", err)
	}
	policy.Bindings = mergeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Projects.Topics.SetIamPolicy(name, &pubsub.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}

// mergeIAMBinding appends member to the existing binding for role if one
// exists (skipping if already granted), or appends a brand-new role binding
// otherwise. It never removes or replaces any other binding in the slice.
func mergeIAMBinding(bindings []*pubsub.Binding, role, member string) []*pubsub.Binding {
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
	return append(bindings, &pubsub.Binding{Role: role, Members: []string{member}})
}

// UpdateSubscriptionAckDeadline patches a subscription's ack deadline,
// matching `gcloud pubsub subscriptions update --ack-deadline`. Push config,
// retention, and dead-letter policy changes are out of scope for this
// minimal Update flow.
func (c *Client) UpdateSubscriptionAckDeadline(projectID, subID string, ackDeadline int) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	req := &pubsub.UpdateSubscriptionRequest{
		Subscription: &pubsub.Subscription{
			Name:               name,
			AckDeadlineSeconds: int64(ackDeadline),
			ForceSendFields:    []string{"AckDeadlineSeconds"},
		},
		UpdateMask: "ackDeadlineSeconds",
	}
	_, err := c.service.Projects.Subscriptions.Patch(name, req).Do()
	return err
}

// -----------------------------------------------------------------------------
// Data-plane
// -----------------------------------------------------------------------------

// Publish sends a single text message to a topic, matching
// `gcloud pubsub topics publish --message`. Returns the server-assigned
// message ID on success.
func (c *Client) Publish(projectID, topicID, message string) (string, error) {
	if demo.Enabled {
		return "demo-message-id", nil
	}
	if c.service == nil {
		return "", fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/topics/%s", projectID, topicID)
	req := &pubsub.PublishRequest{
		Messages: []*pubsub.PubsubMessage{
			{Data: base64.StdEncoding.EncodeToString([]byte(message))},
		},
	}
	resp, err := c.service.Projects.Topics.Publish(name, req).Do()
	if err != nil {
		return "", err
	}
	if len(resp.MessageIds) == 0 {
		return "", nil
	}
	return resp.MessageIds[0], nil
}

// PulledMessage is a single message returned by a Pull call, decoded for
// display. It is never acknowledged automatically -- AckID is retained so a
// later explicit Ack/ModifyAckDeadline call can reference it.
type PulledMessage struct {
	MessageID   string
	AckID       string
	Data        string
	PublishTime string
	Attributes  map[string]string
}

// Pull fetches up to maxMessages currently-available messages from a
// subscription via the raw `Subscriptions.Pull` RPC, matching
// `gcloud pubsub subscriptions pull` — WITHOUT acknowledging them. Unlike the
// streaming Receive() in the cloud.google.com/go/pubsub client library, a
// single Pull RPC has no ack side-effect unless AckIds are explicitly sent
// back, which this method never does.
func (c *Client) Pull(projectID, subID string, maxMessages int64) ([]PulledMessage, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	req := &pubsub.PullRequest{
		MaxMessages:       maxMessages,
		ReturnImmediately: true,
	}
	resp, err := c.service.Projects.Subscriptions.Pull(name, req).Do()
	if err != nil {
		return nil, err
	}
	out := make([]PulledMessage, 0, len(resp.ReceivedMessages))
	for _, rm := range resp.ReceivedMessages {
		if rm.Message == nil {
			continue
		}
		data := rm.Message.Data
		if decoded, err := base64.StdEncoding.DecodeString(data); err == nil {
			data = string(decoded)
		}
		out = append(out, PulledMessage{
			MessageID:   rm.Message.MessageId,
			AckID:       rm.AckId,
			Data:        data,
			PublishTime: rm.Message.PublishTime,
			Attributes:  rm.Message.Attributes,
		})
	}
	return out, nil
}

// Ack acknowledges the given ack IDs on a subscription, matching
// `gcloud pubsub subscriptions ack`. Only meaningful for messages just
// obtained via Pull -- ack IDs expire and are subscription-specific.
func (c *Client) Ack(projectID, subID string, ackIDs []string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	if len(ackIDs) == 0 {
		return nil
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	_, err := c.service.Projects.Subscriptions.Acknowledge(name, &pubsub.AcknowledgeRequest{AckIds: ackIDs}).Do()
	return err
}

// ModifyAckDeadline extends (or shortens) the ack deadline for the given ack
// IDs on a subscription, matching
// `gcloud pubsub subscriptions modify-message-ack-deadline`.
func (c *Client) ModifyAckDeadline(projectID, subID string, ackIDs []string, ackDeadlineSeconds int64) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	if len(ackIDs) == 0 {
		return nil
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	req := &pubsub.ModifyAckDeadlineRequest{AckIds: ackIDs, AckDeadlineSeconds: ackDeadlineSeconds}
	_, err := c.service.Projects.Subscriptions.ModifyAckDeadline(name, req).Do()
	return err
}

// SeekToTime resets a subscription's delivery cursor to the given time,
// matching `gcloud pubsub subscriptions seek --time=TIMESTAMP`. All messages
// published after that time (and any not-yet-expired retained messages
// before it) become re-deliverable.
func (c *Client) SeekToTime(projectID, subID string, t time.Time) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	req := &pubsub.SeekRequest{Time: t.UTC().Format(time.RFC3339)}
	_, err := c.service.Projects.Subscriptions.Seek(name, req).Do()
	return err
}

// GetSubscriptionIAMPolicy reads a subscription's current IAM policy,
// matching `gcloud pubsub subscriptions get-iam-policy`.
func (c *Client) GetSubscriptionIAMPolicy(projectID, subID string) ([]IAMBinding, error) {
	if demo.Enabled {
		return nil, nil
	}
	if c.service == nil {
		return nil, fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	policy, err := c.service.Projects.Subscriptions.GetIamPolicy(name).Do()
	if err != nil {
		return nil, fmt.Errorf("get subscription IAM policy: %w", err)
	}
	var out []IAMBinding
	for _, b := range policy.Bindings {
		out = append(out, IAMBinding{Role: b.Role, Members: b.Members})
	}
	return out, nil
}

// AddSubscriptionIAMBinding grants a role to a member on a subscription,
// matching `gcloud pubsub subscriptions add-iam-policy-binding`. Like
// AddTopicIAMBinding, this merges into the existing policy and never drops
// another binding.
func (c *Client) AddSubscriptionIAMBinding(projectID, subID, role, member string) error {
	if demo.Enabled {
		return nil
	}
	if c.service == nil {
		return fmt.Errorf("pubsub client not initialized")
	}
	name := fmt.Sprintf("projects/%s/subscriptions/%s", projectID, subID)
	policy, err := c.service.Projects.Subscriptions.GetIamPolicy(name).Do()
	if err != nil {
		return fmt.Errorf("get subscription IAM policy: %w", err)
	}
	policy.Bindings = mergeIAMBinding(policy.Bindings, role, member)
	_, err = c.service.Projects.Subscriptions.SetIamPolicy(name, &pubsub.SetIamPolicyRequest{Policy: policy}).Do()
	return err
}
