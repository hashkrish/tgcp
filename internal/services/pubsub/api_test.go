package pubsub

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestMergeIAMBinding(t *testing.T) {
	tests := []struct {
		name     string
		bindings []*pubsub.Binding
		role     string
		member   string
		want     []*pubsub.Binding
	}{
		{
			name:     "Empty bindings",
			bindings: nil,
			role:     "roles/pubsub.publisher",
			member:   "user:test@example.com",
			want: []*pubsub.Binding{
				{Role: "roles/pubsub.publisher", Members: []string{"user:test@example.com"}},
			},
		},
		{
			name: "New role",
			bindings: []*pubsub.Binding{
				{Role: "roles/pubsub.viewer", Members: []string{"user:viewer@example.com"}},
			},
			role:   "roles/pubsub.publisher",
			member: "user:test@example.com",
			want: []*pubsub.Binding{
				{Role: "roles/pubsub.viewer", Members: []string{"user:viewer@example.com"}},
				{Role: "roles/pubsub.publisher", Members: []string{"user:test@example.com"}},
			},
		},
		{
			name: "Existing role, new member",
			bindings: []*pubsub.Binding{
				{Role: "roles/pubsub.publisher", Members: []string{"user:existing@example.com"}},
			},
			role:   "roles/pubsub.publisher",
			member: "user:test@example.com",
			want: []*pubsub.Binding{
				{Role: "roles/pubsub.publisher", Members: []string{"user:existing@example.com", "user:test@example.com"}},
			},
		},
		{
			name: "Existing role, existing member",
			bindings: []*pubsub.Binding{
				{Role: "roles/pubsub.publisher", Members: []string{"user:test@example.com"}},
			},
			role:   "roles/pubsub.publisher",
			member: "user:test@example.com",
			want: []*pubsub.Binding{
				{Role: "roles/pubsub.publisher", Members: []string{"user:test@example.com"}},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeIAMBinding(tt.bindings, tt.role, tt.member); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeIAMBinding() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClient_AddTopicIAMBinding(t *testing.T) {
	var setPolicyCalled bool
	var setPolicyPayload string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:getIamPolicy" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bindings": [{"role": "roles/pubsub.publisher", "members": ["user:existing@example.com"]}]}`))
			return
		}

		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:setIamPolicy" {
			setPolicyCalled = true

			body, _ := io.ReadAll(r.Body)
			setPolicyPayload = string(body)

			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"bindings": [{"role": "roles/pubsub.publisher", "members": ["user:existing@example.com", "user:new@example.com"]}]}`))
			return
		}

		t.Fatalf("Unexpected request path: %s", r.URL.Path)
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}
	err = client.AddTopicIAMBinding("my-project", "my-topic", "roles/pubsub.publisher", "user:new@example.com")
	if err != nil {
		t.Fatalf("AddTopicIAMBinding failed: %v", err)
	}

	if !setPolicyCalled {
		t.Errorf("setIamPolicy was not called")
	}

	expectedSubstring := `"role":"roles/pubsub.publisher"`
	if !strings.Contains(setPolicyPayload, expectedSubstring) {
		t.Errorf("Expected payload to contain %q, but got: %s", expectedSubstring, setPolicyPayload)
	}
	expectedSubstring2 := `"user:new@example.com"`
	if !strings.Contains(setPolicyPayload, expectedSubstring2) {
		t.Errorf("Expected payload to contain %q, but got: %s", expectedSubstring2, setPolicyPayload)
	}
}
