package pubsub

import (
	"context"
	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
	pubsubapi "google.golang.org/api/pubsub/v1"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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

func TestClient_GetSubscriptionIAMPolicy(t *testing.T) {
	originalDemoEnabled := demo.Enabled
	defer func() { demo.Enabled = originalDemoEnabled }()

	tests := []struct {
		name          string
		demoEnabled   bool
		clientInit    bool
		mockStatus    int
		mockBody      string
		projectID     string
		subID         string
		wantBindings  []IAMBinding
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name:         "demo mode enabled",
			demoEnabled:  true,
			clientInit:   true,
			projectID:    "proj-1",
			subID:        "sub-1",
			wantBindings: nil,
			wantErr:      false,
		},
		{
			name:          "uninitialized client",
			demoEnabled:   false,
			clientInit:    false,
			projectID:     "proj-1",
			subID:         "sub-1",
			wantErr:       true,
			wantErrSubstr: "pubsub client not initialized",
		},
		{
			name:        "success with bindings",
			demoEnabled: false,
			clientInit:  true,
			mockStatus:  http.StatusOK,
			mockBody: `{
				"bindings": [
					{
						"role": "roles/pubsub.editor",
						"members": ["user:alice@example.com", "serviceAccount:sa@example.com"]
					},
					{
						"role": "roles/pubsub.viewer",
						"members": ["user:bob@example.com"]
					}
				]
			}`,
			projectID: "my-project",
			subID:     "my-sub",
			wantBindings: []IAMBinding{
				{Role: "roles/pubsub.editor", Members: []string{"user:alice@example.com", "serviceAccount:sa@example.com"}},
				{Role: "roles/pubsub.viewer", Members: []string{"user:bob@example.com"}},
			},
			wantErr: false,
		},
		{
			name:         "success with empty bindings",
			demoEnabled:  false,
			clientInit:   true,
			mockStatus:   http.StatusOK,
			mockBody:     `{}`,
			projectID:    "my-project",
			subID:        "my-sub",
			wantBindings: nil,
			wantErr:      false,
		},
		{
			name:          "API error",
			demoEnabled:   false,
			clientInit:    true,
			mockStatus:    http.StatusForbidden,
			mockBody:      `{"error": {"message": "Permission denied"}}`,
			projectID:     "my-project",
			subID:         "my-sub",
			wantErr:       true,
			wantErrSubstr: "get subscription IAM policy",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demo.Enabled = tt.demoEnabled

			var client *Client

			if tt.clientInit {
				// Set up mock HTTP server
				mux := http.NewServeMux()
				mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
					// Verify request path for getIamPolicy
					expectedPath := "/v1/projects/" + tt.projectID + "/subscriptions/" + tt.subID + ":getIamPolicy"
					if r.URL.Path != expectedPath {
						t.Errorf("Unexpected request path: got %s, want %s", r.URL.Path, expectedPath)
						w.WriteHeader(http.StatusNotFound)
						w.Write([]byte(`{"error": "not found"}`))
						return
					}

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.mockStatus)
					w.Write([]byte(tt.mockBody))
				})
				ts := httptest.NewServer(mux)
				defer ts.Close()

				ctx := context.Background()
				svc, err := pubsubapi.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
				if err != nil {
					t.Fatalf("failed to create pubsub service: %v", err)
				}
				client = &Client{service: svc}
			} else {
				client = &Client{service: nil}
			}

			gotBindings, err := client.GetSubscriptionIAMPolicy(tt.projectID, tt.subID)

			if (err != nil) != tt.wantErr {
				t.Errorf("GetSubscriptionIAMPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.wantErrSubstr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Errorf("GetSubscriptionIAMPolicy() error = %v, want substring %q", err, tt.wantErrSubstr)
				}
			}

			if !reflect.DeepEqual(gotBindings, tt.wantBindings) {
				t.Errorf("GetSubscriptionIAMPolicy() = %v, want %v", gotBindings, tt.wantBindings)
			}
		})
	}
}
