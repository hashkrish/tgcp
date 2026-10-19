package pubsub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestDeleteSubscription(t *testing.T) {
	tests := []struct {
		name       string
		projectID  string
		subID      string
		mockStatus int
		mockBody   string
		wantErr    bool
	}{
		{
			name:       "success",
			projectID:  "my-project",
			subID:      "my-sub",
			mockStatus: http.StatusOK,
			mockBody:   `{}`,
			wantErr:    false,
		},
		{
			name:       "not found error",
			projectID:  "my-project",
			subID:      "non-existent-sub",
			mockStatus: http.StatusNotFound,
			mockBody:   `{"error": {"code": 404, "message": "Resource not found"}}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expectedPath := "/v1/projects/" + tt.projectID + "/subscriptions/" + tt.subID
				if r.URL.Path != expectedPath {
					t.Errorf("expected path %q, got %q", expectedPath, r.URL.Path)
				}
				if r.Method != "DELETE" {
					t.Errorf("expected method DELETE, got %q", r.Method)
				}
				w.WriteHeader(tt.mockStatus)
				w.Write([]byte(tt.mockBody))
			}))
			defer server.Close()

			ctx := context.Background()
			svc, err := pubsub.NewService(ctx, option.WithEndpoint(server.URL), option.WithoutAuthentication())
			if err != nil {
				t.Fatalf("failed to create pubsub service: %v", err)
			}

			client := &Client{service: svc}

			err = client.DeleteSubscription(tt.projectID, tt.subID)

			if (err != nil) != tt.wantErr {
				t.Errorf("DeleteSubscription() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestDeleteSubscription_Uninitialized(t *testing.T) {
	client := &Client{service: nil}
	err := client.DeleteSubscription("my-project", "my-sub")
	if err == nil {
		t.Error("expected error when service is nil, got nil")
	}
}
