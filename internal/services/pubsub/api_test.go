package pubsub

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestPull(t *testing.T) {
	tests := []struct {
		name        string
		mockResp    string
		mockStatus  int
		wantCount   int
		wantErr     bool
		checkOutput func(t *testing.T, msgs []PulledMessage)
	}{
		{
			name:       "success with mixed messages (encoded and empty)",
			mockStatus: http.StatusOK,
			mockResp: `{
				"receivedMessages": [
					{
						"ackId": "ack1",
						"message": {
							"messageId": "msg1",
							"data": "` + base64.StdEncoding.EncodeToString([]byte("test data 1")) + `",
							"publishTime": "2023-10-27T10:00:00Z",
							"attributes": {
								"key1": "value1"
							}
						}
					},
					{
						"ackId": "ack2",
						"message": {
							"messageId": "msg2",
							"data": "plain text fallback",
							"publishTime": "2023-10-27T10:01:00Z"
						}
					},
					{
						"ackId": "ack3"
					}
				]
			}`,
			wantCount: 2,
			wantErr:   false,
			checkOutput: func(t *testing.T, msgs []PulledMessage) {
				if msgs[0].MessageID != "msg1" || msgs[0].Data != "test data 1" || msgs[0].AckID != "ack1" {
					t.Errorf("unexpected message 1: %+v", msgs[0])
				}
				if msgs[0].Attributes["key1"] != "value1" {
					t.Errorf("unexpected attributes 1: %+v", msgs[0].Attributes)
				}
				if msgs[1].MessageID != "msg2" || msgs[1].Data != "plain text fallback" || msgs[1].AckID != "ack2" {
					t.Errorf("unexpected message 2: %+v", msgs[1])
				}
			},
		},
		{
			name:       "empty response",
			mockStatus: http.StatusOK,
			mockResp:   `{}`,
			wantCount:  0,
			wantErr:    false,
		},
		{
			name:       "api error",
			mockStatus: http.StatusInternalServerError,
			mockResp:   `{"error": {"message": "internal server error"}}`,
			wantCount:  0,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.mockStatus)
				fmt.Fprintln(w, tt.mockResp)
			}))
			defer testServer.Close()

			ctx := context.Background()
			svc, err := pubsub.NewService(ctx, option.WithEndpoint(testServer.URL), option.WithoutAuthentication())
			if err != nil {
				t.Fatalf("failed to create mock pubsub service: %v", err)
			}

			client := &Client{service: svc}

			messages, err := client.Pull("test-project", "test-sub", 10)

			if (err != nil) != tt.wantErr {
				t.Errorf("Pull() error = %v, wantErr %v", err, tt.wantErr)
			}

			if len(messages) != tt.wantCount {
				t.Errorf("Pull() expected %d messages, got %d", tt.wantCount, len(messages))
			}

			if tt.checkOutput != nil && len(messages) > 0 {
				tt.checkOutput(t, messages)
			}
		})
	}
}

func TestPull_ClientNotInitialized(t *testing.T) {
	client := &Client{service: nil}
	_, err := client.Pull("test-project", "test-sub", 10)
	if err == nil {
		t.Error("Pull() expected error for uninitialized client, got nil")
	}
}
