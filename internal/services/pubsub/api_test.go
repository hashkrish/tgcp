package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestDeleteTopic_DemoMode(t *testing.T) {
	// Ensure we restore demo mode status
	orig := demo.Enabled
	demo.Enabled = true
	defer func() { demo.Enabled = orig }()

	client := &Client{} // Intentionally nil service
	err := client.DeleteTopic("my-project", "my-topic")
	if err != nil {
		t.Errorf("Expected nil error in demo mode, got %v", err)
	}
}

func TestDeleteTopic_UninitializedClient(t *testing.T) {
	// Ensure demo mode is off
	orig := demo.Enabled
	demo.Enabled = false
	defer func() { demo.Enabled = orig }()

	client := &Client{} // Intentionally nil service
	err := client.DeleteTopic("my-project", "my-topic")

	if err == nil {
		t.Fatalf("Expected error for uninitialized client, got nil")
	}
	expectedErr := "pubsub client not initialized"
	if err.Error() != expectedErr {
		t.Errorf("Expected error %q, got %q", expectedErr, err.Error())
	}
}

func TestDeleteTopic_Success(t *testing.T) {
	// Ensure demo mode is off
	orig := demo.Enabled
	demo.Enabled = false
	defer func() { demo.Enabled = orig }()

	called := false
	var gotPath string
	var gotMethod string

	// Create a mock server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		gotPath = r.URL.Path
		gotMethod = r.Method
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	// Initialize a pubsub service pointing to the mock server
	svc, err := pubsub.NewService(
		context.Background(),
		option.WithEndpoint(ts.URL),
		option.WithoutAuthentication(),
	)
	if err != nil {
		t.Fatalf("Failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	err = client.DeleteTopic("test-project", "test-topic")
	if err != nil {
		t.Fatalf("DeleteTopic failed: %v", err)
	}

	if !called {
		t.Fatalf("Expected HTTP call, but none was made")
	}

	expectedPath := "/v1/projects/test-project/topics/test-topic"
	if gotPath != expectedPath {
		t.Errorf("Expected path %q, got %q", expectedPath, gotPath)
	}

	if gotMethod != "DELETE" {
		t.Errorf("Expected method DELETE, got %q", gotMethod)
	}
}
