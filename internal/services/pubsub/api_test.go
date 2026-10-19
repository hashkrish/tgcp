package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"io"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestUpdateSubscriptionAckDeadline(t *testing.T) {
	// 1. Mock server to capture the request
	var capturedBody string
	var capturedPath string
	var capturedMethod string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		capturedPath = r.URL.Path

		body, err := io.ReadAll(r.Body)
		if err == nil {
			capturedBody = string(body)
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) // Send empty JSON object back
	}))
	defer ts.Close()

	// 2. Setup the client using the mock server
	ctx := context.Background()
	svc, err := pubsub.NewService(ctx, option.WithEndpoint(ts.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("Failed to create pubsub service: %v", err)
	}

	client := &Client{service: svc}

	// 3. Call the method being tested
	projectID := "test-project"
	subID := "test-sub"
	ackDeadline := 30

	err = client.UpdateSubscriptionAckDeadline(projectID, subID, ackDeadline)
	if err != nil {
		t.Fatalf("UpdateSubscriptionAckDeadline returned error: %v", err)
	}

	// 4. Verify the captured request
	expectedPath := "/v1/projects/test-project/subscriptions/test-sub"
	if capturedPath != expectedPath {
		t.Errorf("Expected path %q, got %q", expectedPath, capturedPath)
	}

	if capturedMethod != http.MethodPatch {
		t.Errorf("Expected method PATCH, got %q", capturedMethod)
	}

	expectedName := "projects/test-project/subscriptions/test-sub"
	if !strings.Contains(capturedBody, expectedName) {
		t.Errorf("Expected body to contain %q, got %q", expectedName, capturedBody)
	}

	if !strings.Contains(capturedBody, `"ackDeadlineSeconds":30`) {
		t.Errorf("Expected body to contain ackDeadlineSeconds=30, got %q", capturedBody)
	}

	if !strings.Contains(capturedBody, `"updateMask":"ackDeadlineSeconds"`) {
		t.Errorf("Expected body to contain updateMask=ackDeadlineSeconds, got %q", capturedBody)
	}
}

func TestUpdateSubscriptionAckDeadline_DemoMode(t *testing.T) {
	demo.Enabled = true
	defer func() { demo.Enabled = false }()

	client := &Client{}
	err := client.UpdateSubscriptionAckDeadline("p", "s", 10)
	if err != nil {
		t.Errorf("Expected nil error in demo mode, got %v", err)
	}
}

func TestUpdateSubscriptionAckDeadline_NilService(t *testing.T) {
	client := &Client{}
	err := client.UpdateSubscriptionAckDeadline("p", "s", 10)
	if err == nil {
		t.Error("Expected error for nil service, got nil")
	}
	if err.Error() != "pubsub client not initialized" {
		t.Errorf("Expected 'pubsub client not initialized' error, got %v", err)
	}
}
