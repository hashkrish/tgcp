package pubsub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"encoding/base64"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestPublish(t *testing.T) {
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)

		// return a mocked response
		resp := pubsub.PublishResponse{
			MessageIds: []string{"test-msg-id-123"},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	projectID := "test-project"
	topicID := "test-topic"
	message := "hello world"

	msgID, err := client.Publish(projectID, topicID, message)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	if msgID != "test-msg-id-123" {
		t.Errorf("expected msgID 'test-msg-id-123', got %q", msgID)
	}

	// Verify request payload
	var req pubsub.PublishRequest
	if err := json.Unmarshal(gotBody, &req); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}

	expectedData := base64.StdEncoding.EncodeToString([]byte(message))
	if req.Messages[0].Data != expectedData {
		t.Errorf("expected data %q, got %q", expectedData, req.Messages[0].Data)
	}
}

func TestPublish_Error(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	projectID := "test-project"
	topicID := "test-topic"
	message := "hello world"

	msgID, err := client.Publish(projectID, topicID, message)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if msgID != "" {
		t.Errorf("expected empty msgID on error, got %q", msgID)
	}
}

func TestPublish_EmptyMessageIds(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := pubsub.PublishResponse{
			MessageIds: []string{}, // Empty
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	projectID := "test-project"
	topicID := "test-topic"
	message := "hello world"

	msgID, err := client.Publish(projectID, topicID, message)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if msgID != "" {
		t.Errorf("expected empty msgID when no message IDs are returned, got %q", msgID)
	}
}

func TestPublish_NilService(t *testing.T) {
	client := &Client{service: nil}

	projectID := "test-project"
	topicID := "test-topic"
	message := "hello world"

	msgID, err := client.Publish(projectID, topicID, message)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if err.Error() != "pubsub client not initialized" {
		t.Errorf("expected error 'pubsub client not initialized', got %q", err.Error())
	}

	if msgID != "" {
		t.Errorf("expected empty msgID on error, got %q", msgID)
	}
}
