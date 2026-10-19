package pubsub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestClient_DetachSubscription(t *testing.T) {
	t.Run("demo mode", func(t *testing.T) {
		demo.Enabled = true
		defer func() { demo.Enabled = false }()

		c := &Client{}
		err := c.DetachSubscription("test-project", "test-sub")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("uninitialized client", func(t *testing.T) {
		demo.Enabled = false
		c := &Client{service: nil}
		err := c.DetachSubscription("test-project", "test-sub")
		if err == nil {
			t.Errorf("expected error, got nil")
		} else if err.Error() != "pubsub client not initialized" {
			t.Errorf("expected uninitialized error, got %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		demo.Enabled = false
		rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			expectedPath := "/v1/projects/test-project/subscriptions/test-sub:detach"
			if req.URL.Path != expectedPath {
				t.Errorf("expected path %q, got %q", expectedPath, req.URL.Path)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte("{}"))),
			}, nil
		})

		svc, err := pubsub.NewService(context.Background(), option.WithHTTPClient(&http.Client{Transport: rt}))
		if err != nil {
			t.Fatalf("failed to create pubsub service: %v", err)
		}

		c := &Client{service: svc}
		err = c.DetachSubscription("test-project", "test-sub")
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("error from API", func(t *testing.T) {
		demo.Enabled = false
		rt := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"error": {"message": "internal error"}}`))),
			}, nil
		})

		svc, err := pubsub.NewService(context.Background(), option.WithHTTPClient(&http.Client{Transport: rt}))
		if err != nil {
			t.Fatalf("failed to create pubsub service: %v", err)
		}

		c := &Client{service: svc}
		err = c.DetachSubscription("test-project", "test-sub")
		if err == nil {
			t.Errorf("expected error, got nil")
		}
	})
}
