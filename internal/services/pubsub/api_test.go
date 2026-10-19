package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"io"
	"encoding/json"
	"strings"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestCreateSubscription(t *testing.T) {
	var requestedMethod string
	var requestedURL string
	var requestBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedMethod = r.Method
		requestedURL = r.URL.String()

		body, _ := io.ReadAll(r.Body)
		requestBody = body

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	client := &Client{service: svc}

	err = client.CreateSubscription("my-project", "my-sub", "my-topic", 60)
	if err != nil {
		t.Fatalf("CreateSubscription failed: %v", err)
	}

	if requestedMethod != "PUT" {
		t.Errorf("expected method PUT, got %q", requestedMethod)
	}

	expectedURLPrefix := "/v1/projects/my-project/subscriptions/my-sub"
	if !strings.HasPrefix(requestedURL, expectedURLPrefix) {
		t.Errorf("expected URL to start with %q, got %q", expectedURLPrefix, requestedURL)
	}

	var reqPayload pubsub.Subscription
	if err := json.Unmarshal(requestBody, &reqPayload); err != nil {
		t.Fatalf("failed to parse request body: %v", err)
	}

	expectedTopic := "projects/my-project/topics/my-topic"
	if reqPayload.Topic != expectedTopic {
		t.Errorf("expected topic %q, got %q", expectedTopic, reqPayload.Topic)
	}

	if reqPayload.AckDeadlineSeconds != 60 {
		t.Errorf("expected AckDeadlineSeconds 60, got %d", reqPayload.AckDeadlineSeconds)
	}
}

func TestCreateSubscription_NoAckDeadline(t *testing.T) {
	var requestBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requestBody = body

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	client := &Client{service: svc}

	err = client.CreateSubscription("my-project", "my-sub", "my-topic", 0)
	if err != nil {
		t.Fatalf("CreateSubscription failed: %v", err)
	}

	var reqPayload pubsub.Subscription
	if err := json.Unmarshal(requestBody, &reqPayload); err != nil {
		t.Fatalf("failed to parse request body: %v", err)
	}

	if reqPayload.AckDeadlineSeconds != 0 {
		t.Errorf("expected AckDeadlineSeconds 0, got %d", reqPayload.AckDeadlineSeconds)
	}
}

func TestCreateSubscription_ClientNotInitialized(t *testing.T) {
	client := &Client{service: nil}
	err := client.CreateSubscription("my-project", "my-sub", "my-topic", 60)
	if err == nil {
		t.Error("expected error when client is not initialized, got nil")
	} else if err.Error() != "pubsub client not initialized" {
		t.Errorf("expected error %q, got %q", "pubsub client not initialized", err.Error())
	}
}

func TestCreateSubscription_DemoMode(t *testing.T) {
	demo.Enabled = true
	defer func() { demo.Enabled = false }()

	client := &Client{service: nil}
	err := client.CreateSubscription("my-project", "my-sub", "my-topic", 60)
	if err != nil {
		t.Errorf("expected no error in demo mode, got %v", err)
	}
}

func TestCreateSubscription_APIError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": {"message": "bad request"}}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}

	client := &Client{service: svc}

	err = client.CreateSubscription("my-project", "my-sub", "my-topic", 60)
	if err == nil {
		t.Error("expected error when API returns error, got nil")
	} else if !strings.Contains(err.Error(), "bad request") {
		t.Errorf("expected error containing %q, got %q", "bad request", err.Error())
	}
}
