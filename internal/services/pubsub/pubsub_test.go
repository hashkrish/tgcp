package pubsub

import (
	"testing"
)

func sampleTopics() []Topic {
	return []Topic{
		{Name: "topic-1", KmsKeyName: "key-1"},
		{Name: "topic-2", KmsKeyName: ""},
		{Name: "important-topic", KmsKeyName: "key-secure"},
	}
}

func sampleSubs() []Subscription {
	return []Subscription{
		{Name: "sub-1", Topic: "topic-1", PushEndpoint: ""},
		{Name: "sub-2", Topic: "topic-2", PushEndpoint: "https://example.com"},
		{Name: "important-sub", Topic: "important-topic", PushEndpoint: ""},
	}
}

func namesOfTopics(topics []Topic) []string {
	if len(topics) == 0 {
		return nil
	}
	names := make([]string, len(topics))
	for i, t := range topics {
		names[i] = t.Name
	}
	return names
}

func namesOfSubs(subs []Subscription) []string {
	if len(subs) == 0 {
		return nil
	}
	names := make([]string, len(subs))
	for i, s := range subs {
		names[i] = s.Name
	}
	return names
}

func TestNewService(t *testing.T) {
	svc := NewService(nil)
	if svc == nil {
		t.Fatalf("NewService() returned nil")
	}
	if svc.table == nil {
		t.Errorf("NewService() expected table to be initialized")
	}
	if svc.viewState != ViewListTopics {
		t.Errorf("NewService() viewState = %v, want %v", svc.viewState, ViewListTopics)
	}
}

func TestService_Name(t *testing.T) {
	svc := NewService(nil)
	want := "Pub/Sub"
	if got := svc.Name(); got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

func TestService_ShortName(t *testing.T) {
	svc := NewService(nil)
	want := "pubsub"
	if got := svc.ShortName(); got != want {
		t.Errorf("ShortName() = %q, want %q", got, want)
	}
}

func TestGetFilteredTopics(t *testing.T) {
	s := NewService(nil)
	topics := sampleTopics()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all", "", []string{"topic-1", "topic-2", "important-topic"}},
		{"matches by name", "topic", []string{"topic-1", "topic-2", "important-topic"}},
		{"matches by name specific", "topic-1", []string{"topic-1"}},
		{"matches by KMS key", "secure", []string{"important-topic"}},
		{"case-insensitive", "IMPORTANT", []string{"important-topic"}},
		{"no match", "does-not-exist", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.getFilteredTopics(topics, tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("getFilteredTopics(%q) = %v, want %v", tt.query, namesOfTopics(got), tt.want)
			}
			for i, topic := range got {
				if topic.Name != tt.want[i] {
					t.Fatalf("getFilteredTopics(%q) = %v, want %v", tt.query, namesOfTopics(got), tt.want)
				}
			}
		})
	}
}

func TestGetFilteredSubs(t *testing.T) {
	s := NewService(nil)
	subs := sampleSubs()

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all", "", []string{"sub-1", "sub-2", "important-sub"}},
		{"matches by name", "sub", []string{"sub-1", "sub-2", "important-sub"}},
		{"matches by name specific", "sub-1", []string{"sub-1"}},
		{"matches by topic", "important", []string{"important-sub"}},
		{"matches by push endpoint", "example.com", []string{"sub-2"}},
		{"case-insensitive", "IMPORTANT", []string{"important-sub"}},
		{"no match", "does-not-exist", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.getFilteredSubs(subs, tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("getFilteredSubs(%q) = %v, want %v", tt.query, namesOfSubs(got), tt.want)
			}
			for i, sub := range got {
				if sub.Name != tt.want[i] {
					t.Fatalf("getFilteredSubs(%q) = %v, want %v", tt.query, namesOfSubs(got), tt.want)
				}
			}
		})
	}
}
