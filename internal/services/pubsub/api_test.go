package pubsub

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/api/option"
	"google.golang.org/api/pubsub/v1"
)

func TestAddSubscriptionIAMBinding(t *testing.T) {
	var getCalled, setCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/subscriptions/my-sub:getIamPolicy" {
			getCalled = true
			policy := &pubsub.Policy{
				Bindings: []*pubsub.Binding{
					{Role: "roles/pubsub.viewer", Members: []string{"user:foo@example.com"}},
				},
				Etag: "etag123",
			}
			json.NewEncoder(w).Encode(policy)
			return
		}
		if r.URL.Path == "/v1/projects/my-project/subscriptions/my-sub:setIamPolicy" {
			setCalled = true
			var req pubsub.SetIamPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode SetIamPolicyRequest: %v", err)
			}
			if len(req.Policy.Bindings) != 2 {
				t.Errorf("expected 2 bindings, got %d", len(req.Policy.Bindings))
			}

			// check that we added the new role
			found := false
			for _, b := range req.Policy.Bindings {
				if b.Role == "roles/pubsub.editor" {
					found = true
					if len(b.Members) != 1 || b.Members[0] != "user:bar@example.com" {
						t.Errorf("unexpected members: %v", b.Members)
					}
				}
			}
			if !found {
				t.Errorf("new role not added to policy")
			}

			json.NewEncoder(w).Encode(req.Policy)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddSubscriptionIAMBinding("my-project", "my-sub", "roles/pubsub.editor", "user:bar@example.com")
	if err != nil {
		t.Fatalf("AddSubscriptionIAMBinding returned error: %v", err)
	}
	if !getCalled {
		t.Error("GetIamPolicy was not called")
	}
	if !setCalled {
		t.Error("SetIamPolicy was not called")
	}
}

func TestAddSubscriptionIAMBinding_ClientNotInitialized(t *testing.T) {
	client := &Client{service: nil}
	err := client.AddSubscriptionIAMBinding("my-project", "my-sub", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error for uninitialized client, got nil")
	}
}

func TestAddSubscriptionIAMBinding_GetIamPolicyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/subscriptions/my-sub:getIamPolicy" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddSubscriptionIAMBinding("my-project", "my-sub", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error when GetIamPolicy fails, got nil")
	}
}

func TestAddSubscriptionIAMBinding_SetIamPolicyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/subscriptions/my-sub:getIamPolicy" {
			policy := &pubsub.Policy{
				Bindings: []*pubsub.Binding{},
				Etag:     "etag123",
			}
			json.NewEncoder(w).Encode(policy)
			return
		}
		if r.URL.Path == "/v1/projects/my-project/subscriptions/my-sub:setIamPolicy" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddSubscriptionIAMBinding("my-project", "my-sub", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error when SetIamPolicy fails, got nil")
	}
}

func TestAddTopicIAMBinding(t *testing.T) {
	var getCalled, setCalled bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:getIamPolicy" {
			getCalled = true
			policy := &pubsub.Policy{
				Bindings: []*pubsub.Binding{
					{Role: "roles/pubsub.viewer", Members: []string{"user:foo@example.com"}},
				},
				Etag: "etag123",
			}
			json.NewEncoder(w).Encode(policy)
			return
		}
		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:setIamPolicy" {
			setCalled = true
			var req pubsub.SetIamPolicyRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode SetIamPolicyRequest: %v", err)
			}
			if len(req.Policy.Bindings) != 2 {
				t.Errorf("expected 2 bindings, got %d", len(req.Policy.Bindings))
			}

			// check that we added the new role
			found := false
			for _, b := range req.Policy.Bindings {
				if b.Role == "roles/pubsub.editor" {
					found = true
					if len(b.Members) != 1 || b.Members[0] != "user:bar@example.com" {
						t.Errorf("unexpected members: %v", b.Members)
					}
				}
			}
			if !found {
				t.Errorf("new role not added to policy")
			}

			json.NewEncoder(w).Encode(req.Policy)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddTopicIAMBinding("my-project", "my-topic", "roles/pubsub.editor", "user:bar@example.com")
	if err != nil {
		t.Fatalf("AddTopicIAMBinding returned error: %v", err)
	}
	if !getCalled {
		t.Error("GetIamPolicy was not called")
	}
	if !setCalled {
		t.Error("SetIamPolicy was not called")
	}
}

func TestAddTopicIAMBinding_ClientNotInitialized(t *testing.T) {
	client := &Client{service: nil}
	err := client.AddTopicIAMBinding("my-project", "my-topic", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error for uninitialized client, got nil")
	}
}

func TestAddTopicIAMBinding_GetIamPolicyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:getIamPolicy" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddTopicIAMBinding("my-project", "my-topic", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error when GetIamPolicy fails, got nil")
	}
}

func TestAddTopicIAMBinding_SetIamPolicyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:getIamPolicy" {
			policy := &pubsub.Policy{
				Bindings: []*pubsub.Binding{},
				Etag:     "etag123",
			}
			json.NewEncoder(w).Encode(policy)
			return
		}
		if r.URL.Path == "/v1/projects/my-project/topics/my-topic:setIamPolicy" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	svc, err := pubsub.NewService(context.Background(), option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	client := &Client{service: svc}

	err = client.AddTopicIAMBinding("my-project", "my-topic", "roles/pubsub.editor", "user:bar@example.com")
	if err == nil {
		t.Error("expected error when SetIamPolicy fails, got nil")
	}
}
