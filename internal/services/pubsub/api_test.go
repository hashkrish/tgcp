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

func TestCreateTopic(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT method, got %s", r.Method)
		}
		expectedPath := "/v1/projects/my-project/topics/my-topic"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %s, got %s", expectedPath, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(&pubsub.Topic{
			Name: "projects/my-project/topics/my-topic",
		})
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create pubsub service: %v", err)
	}
	client := &Client{service: svc}

	err = client.CreateTopic("my-project", "my-topic")
	if err != nil {
		t.Fatalf("CreateTopic returned unexpected error: %v", err)
	}
	if !called {
		t.Error("expected mock server to be called")
	}
}

func TestCreateTopic_UninitializedClient(t *testing.T) {
	client := &Client{}
	err := client.CreateTopic("my-project", "my-topic")
	if err == nil {
		t.Error("expected error, got nil")
	}
	if err.Error() != "pubsub client not initialized" {
		t.Errorf("expected 'pubsub client not initialized' error, got %v", err)
	}
}

func TestCreateTopic_DemoEnabled(t *testing.T) {
	demo.Enabled = true
	defer func() { demo.Enabled = false }()

	client := &Client{}
	err := client.CreateTopic("my-project", "my-topic")
	if err != nil {
		t.Errorf("expected no error in demo mode, got %v", err)
	}
}

func TestCreateTopic_Error(t *testing.T) {
	called := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"code":    500,
				"message": "internal server error",
			},
		})
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create pubsub service: %v", err)
	}
	client := &Client{service: svc}

	err = client.CreateTopic("my-project", "my-topic")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !called {
		t.Error("expected mock server to be called")
	}
}
