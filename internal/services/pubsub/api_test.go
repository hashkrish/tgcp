package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestMergeIAMBinding(t *testing.T) {
	tests := []struct {
		name     string
		bindings []*pubsub.Binding
		role     string
		member   string
		expected []*pubsub.Binding
	}{
		{
			name:     "empty bindings",
			bindings: nil,
			role:     "roles/pubsub.publisher",
			member:   "user:test@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
		},
		{
			name: "role exists, member exists",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:test@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
		},
		{
			name: "role exists, member missing",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com", "user:other@example.com"},
				},
			},
		},
		{
			name: "role does not exist",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.subscriber",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
				{
					Role:    "roles/pubsub.subscriber",
					Members: []string{"user:other@example.com"},
				},
			},
		},
		{
			name: "multiple roles, role exists, member missing",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.admin",
					Members: []string{"user:admin@example.com"},
				},
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.admin",
					Members: []string{"user:admin@example.com"},
				},
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com", "user:other@example.com"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeIAMBinding(tt.bindings, tt.role, tt.member)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("mergeIAMBinding() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestListSubscriptions(t *testing.T) {
	tests := []struct {
		name          string
		demoEnabled   bool
		mockResponses []string
		mockStatus    int
		wantLen       int
		wantErr       bool
		checkSub      func(t *testing.T, subs []Subscription)
	}{
		{
			name:        "demo enabled",
			demoEnabled: true,
			wantLen:     0,
			wantErr:     false,
		},
		{
			name:        "success with mapping",
			demoEnabled: false,
			mockStatus:  http.StatusOK,
			mockResponses: []string{`{
				"subscriptions": [
					{
						"name": "projects/my-project/subscriptions/sub1",
						"topic": "projects/my-project/topics/topic1",
						"pushConfig": {
							"pushEndpoint": "https://example.com/push"
						},
						"ackDeadlineSeconds": 10,
						"retainAckedMessages": true,
						"messageRetentionDuration": "86400s",
						"state": "ACTIVE"
					},
					{
						"name": "projects/my-project/subscriptions/sub2",
						"topic": "projects/my-project/topics/topic2",
						"deadLetterPolicy": {
							"deadLetterTopic": "projects/my-project/topics/dlq-topic"
						},
						"ackDeadlineSeconds": 20
					}
				]
			}`},
			wantLen: 2,
			wantErr: false,
			checkSub: func(t *testing.T, subs []Subscription) {
				sub1 := subs[0]
				if sub1.Name != "sub1" {
					t.Errorf("expected sub1 name to be sub1, got %q", sub1.Name)
				}
				if sub1.Topic != "topic1" {
					t.Errorf("expected sub1 topic to be topic1, got %q", sub1.Topic)
				}
				if sub1.PushEndpoint != "https://example.com/push" {
					t.Errorf("expected sub1 pushEndpoint to be https://example.com/push, got %q", sub1.PushEndpoint)
				}
				if sub1.AckDeadline != 10 {
					t.Errorf("expected sub1 ackDeadline to be 10, got %d", sub1.AckDeadline)
				}
				if !sub1.RetainAcked {
					t.Errorf("expected sub1 retainAcked to be true")
				}
				if sub1.RetentionDuration != "86400s" {
					t.Errorf("expected sub1 retentionDuration to be 86400s, got %q", sub1.RetentionDuration)
				}
				if sub1.State != "ACTIVE" {
					t.Errorf("expected sub1 state to be ACTIVE, got %q", sub1.State)
				}

				sub2 := subs[1]
				if sub2.Name != "sub2" {
					t.Errorf("expected sub2 name to be sub2, got %q", sub2.Name)
				}
				if sub2.DeadLetterTopic != "dlq-topic" {
					t.Errorf("expected sub2 dlq topic to be dlq-topic, got %q", sub2.DeadLetterTopic)
				}
			},
		},
		{
			name:        "api error",
			demoEnabled: false,
			mockStatus:  http.StatusInternalServerError,
			mockResponses: []string{`{
				"error": {
					"code": 500,
					"message": "Internal Server Error"
				}
			}`},
			wantLen: 0,
			wantErr: true,
		},
		{
			name:        "pagination",
			demoEnabled: false,
			mockStatus:  http.StatusOK,
			mockResponses: []string{
				`{
					"subscriptions": [
						{
							"name": "projects/my-project/subscriptions/sub1",
							"topic": "projects/my-project/topics/topic1"
						}
					],
					"nextPageToken": "page2"
				}`,
				`{
					"subscriptions": [
						{
							"name": "projects/my-project/subscriptions/sub2",
							"topic": "projects/my-project/topics/topic2"
						}
					]
				}`,
			},
			wantLen: 2,
			wantErr: false,
			checkSub: func(t *testing.T, subs []Subscription) {
				if subs[0].Name != "sub1" {
					t.Errorf("expected first sub to be sub1, got %q", subs[0].Name)
				}
				if subs[1].Name != "sub2" {
					t.Errorf("expected second sub to be sub2, got %q", subs[1].Name)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demo.Enabled = tt.demoEnabled
			defer func() { demo.Enabled = false }()

			reqIndex := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.mockStatus)
				if reqIndex < len(tt.mockResponses) {
					w.Write([]byte(tt.mockResponses[reqIndex]))
					reqIndex++
				}
			}))
			defer server.Close()

			ctx := context.Background()
			svc, err := pubsub.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
			if err != nil {
				t.Fatalf("failed to create pubsub service: %v", err)
			}

			client := &Client{service: svc}
			subs, err := client.ListSubscriptions("my-project")

			if (err != nil) != tt.wantErr {
				t.Fatalf("ListSubscriptions() error = %v, wantErr %v", err, tt.wantErr)
			}

			if len(subs) != tt.wantLen {
				t.Fatalf("expected %d subscriptions, got %d", tt.wantLen, len(subs))
			}

			if tt.checkSub != nil {
				tt.checkSub(t, subs)
			}
		})
	}
}
