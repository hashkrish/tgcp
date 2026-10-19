package pubsub

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

// mockRoundTripper intercepts HTTP requests and allows custom handling.
type mockRoundTripper struct {
	roundTripFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.roundTripFunc(req)
}

func TestSeekToTime(t *testing.T) {
	// Need a helper to build a client with the mock service
	newTestClient := func(rt http.RoundTripper) *Client {
		httpClient := &http.Client{Transport: rt}
		ctx := context.Background()
		service, err := pubsub.NewService(ctx, option.WithHTTPClient(httpClient))
		if err != nil {
			t.Fatalf("Failed to create mock service: %v", err)
		}
		return &Client{service: service}
	}

	tests := []struct {
		name      string
		client    *Client
		projectID string
		subID     string
		target    time.Time
		wantPath  string
		wantBody  string
		wantErr   bool
	}{
		{
			name:      "client not initialized",
			client:    &Client{service: nil}, // explicitly nil service
			projectID: "my-project",
			subID:     "my-sub",
			target:    time.Date(2023, 10, 15, 12, 0, 0, 0, time.UTC),
			wantErr:   true,
		},
		{
			name:      "basic seek",
			projectID: "my-project",
			subID:     "my-sub",
			target:    time.Date(2023, 10, 15, 12, 0, 0, 0, time.UTC),
			wantPath:  "/v1/projects/my-project/subscriptions/my-sub:seek",
			wantBody:  `{"time":"2023-10-15T12:00:00Z"}` + "\n",
			wantErr:   false,
		},
		{
			name:      "fractional seconds",
			projectID: "other-project",
			subID:     "other-sub",
			target:    time.Date(2023, 10, 15, 12, 0, 0, 500000000, time.UTC),
			wantPath:  "/v1/projects/other-project/subscriptions/other-sub:seek",
			wantBody:  `{"time":"2023-10-15T12:00:00Z"}` + "\n",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath string
			var gotBody string

			rt := &mockRoundTripper{
				roundTripFunc: func(req *http.Request) (*http.Response, error) {
					gotPath = req.URL.Path
					if req.Body != nil {
						bodyBytes, _ := io.ReadAll(req.Body)
						gotBody = string(bodyBytes)
					}
					// Return a dummy success response
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewBufferString(`{}`)),
						Header:     make(http.Header),
					}, nil
				},
			}

			client := tt.client
			if client == nil {
				client = newTestClient(rt)
			}

			err := client.SeekToTime(tt.projectID, tt.subID, tt.target)

			if (err != nil) != tt.wantErr {
				t.Errorf("SeekToTime() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if gotPath != tt.wantPath {
					t.Errorf("Path = %q, want %q", gotPath, tt.wantPath)
				}
				if gotBody != tt.wantBody {
					t.Errorf("Body = %q, want %q", gotBody, tt.wantBody)
				}
			}
		})
	}
}
