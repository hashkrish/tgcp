package pubsub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestListTopics(t *testing.T) {
	// Setup mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request path and method
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET method, got %s", r.Method)
		}
		if r.URL.Path != "/v1/projects/test-project/topics" {
			t.Errorf("Expected path /v1/projects/test-project/topics, got %s", r.URL.Path)
		}

		resp := &pubsub.ListTopicsResponse{
			Topics: []*pubsub.Topic{
				{
					Name:       "projects/test-project/topics/topic-1",
					Labels:     map[string]string{"env": "prod"},
					KmsKeyName: "projects/test-project/locations/global/keyRings/my-key-ring/cryptoKeys/my-key",
				},
				{
					Name: "projects/test-project/topics/topic-2",
				},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	// Test normal execution
	demo.Enabled = false
	topics, err := client.ListTopics("test-project")
	if err != nil {
		t.Fatalf("ListTopics failed: %v", err)
	}

	if len(topics) != 2 {
		t.Fatalf("Expected 2 topics, got %d", len(topics))
	}

	if topics[0].Name != "topic-1" {
		t.Errorf("Expected topic-1, got %s", topics[0].Name)
	}
	if topics[0].ProjectID != "test-project" {
		t.Errorf("Expected project test-project, got %s", topics[0].ProjectID)
	}
	if topics[0].Labels["env"] != "prod" {
		t.Errorf("Expected label env=prod, got %v", topics[0].Labels["env"])
	}
	if topics[0].KmsKeyName != "projects/test-project/locations/global/keyRings/my-key-ring/cryptoKeys/my-key" {
		t.Errorf("Expected specific KmsKeyName, got %s", topics[0].KmsKeyName)
	}

	if topics[1].Name != "topic-2" {
		t.Errorf("Expected topic-2, got %s", topics[1].Name)
	}

	// Test demo mode
	demo.Enabled = true
	demoTopics, err := client.ListTopics("test-project")
	if err != nil {
		t.Fatalf("ListTopics (demo mode) failed: %v", err)
	}
	if len(demoTopics) != 0 {
		t.Errorf("Expected 0 topics in demo mode, got %d", len(demoTopics))
	}
}

func TestListTopicsError(t *testing.T) {
	// Setup mock server that returns an error
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": {"message": "internal error"}}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}
	demo.Enabled = false

	_, err = client.ListTopics("test-project")
	if err == nil {
		t.Fatal("Expected error, got nil")
	}
}
