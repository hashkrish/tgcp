package pubsub

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestClient_Ack(t *testing.T) {
	var reqBody []byte
	var reqPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath = r.URL.Path
		if r.Body != nil {
			var err error
			reqBody, err = io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("failed reading body: %v", err)
			}
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	ctx := context.Background()
	dummyService, err := pubsub.NewService(ctx, option.WithoutAuthentication(), option.WithEndpoint(ts.URL))
	if err != nil {
		t.Fatalf("failed to create dummy service: %v", err)
	}

	t.Run("happy path populates AckRequests with correct IDs", func(t *testing.T) {
		originalDemo := demo.Enabled
		demo.Enabled = false
		defer func() { demo.Enabled = originalDemo }()

		client := &Client{service: dummyService}
		expectedAckIDs := []string{"ack-id-1", "ack-id-2"}

		err := client.Ack("my-project", "my-sub", expectedAckIDs)
		if err != nil {
			t.Fatalf("Ack() unexpectedly returned error: %v", err)
		}

		expectedPath := "/v1/projects/my-project/subscriptions/my-sub:acknowledge"
		if reqPath != expectedPath {
			t.Errorf("expected request path %q, got %q", expectedPath, reqPath)
		}

		var payload struct {
			AckIds []string `json:"ackIds"`
		}
		if err := json.Unmarshal(reqBody, &payload); err != nil {
			t.Fatalf("failed to parse request body %q: %v", string(reqBody), err)
		}

		if !reflect.DeepEqual(payload.AckIds, expectedAckIDs) {
			t.Errorf("expected ackIds %v, got %v", expectedAckIDs, payload.AckIds)
		}
	})

	t.Run("demo mode enabled", func(t *testing.T) {
		originalDemo := demo.Enabled
		demo.Enabled = true
		defer func() { demo.Enabled = originalDemo }()

		client := &Client{service: nil}
		err := client.Ack("test-project", "test-sub", []string{"ack1"})
		if err != nil {
			t.Errorf("expected nil error in demo mode, got %v", err)
		}
	})

	t.Run("client not initialized", func(t *testing.T) {
		originalDemo := demo.Enabled
		demo.Enabled = false
		defer func() { demo.Enabled = originalDemo }()

		client := &Client{service: nil}
		err := client.Ack("test-project", "test-sub", []string{"ack1"})
		if err == nil || err.Error() != "pubsub client not initialized" {
			t.Errorf("expected 'pubsub client not initialized', got %v", err)
		}
	})

	t.Run("empty ackIDs returns nil immediately", func(t *testing.T) {
		originalDemo := demo.Enabled
		demo.Enabled = false
		defer func() { demo.Enabled = originalDemo }()

		reqBody = nil // reset
		client := &Client{service: dummyService}
		err := client.Ack("test-project", "test-sub", []string{})
		if err != nil {
			t.Errorf("expected nil error for empty ackIDs, got %v", err)
		}
		if reqBody != nil {
			t.Errorf("expected no HTTP request to be made, but reqBody was %s", string(reqBody))
		}
	})
}
