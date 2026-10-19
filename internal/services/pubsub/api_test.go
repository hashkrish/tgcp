package pubsub

import (
	"reflect"
	"testing"

	"google.golang.org/api/pubsub/v1"
)

func TestMergeIAMBinding(t *testing.T) {
	tests := []struct {
		name     string
		bindings []*pubsub.Binding
		role     string
		member   string
		expected []*pubsub.Binding
	}{
		{
			name:     "empty bindings",
			bindings: nil,
			role:     "roles/pubsub.publisher",
			member:   "user:test@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
		},
		{
			name: "role exists, member exists",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:test@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
		},
		{
			name: "role exists, member missing",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com", "user:other@example.com"},
				},
			},
		},
		{
			name: "role does not exist",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.subscriber",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
				{
					Role:    "roles/pubsub.subscriber",
					Members: []string{"user:other@example.com"},
				},
			},
		},
		{
			name: "multiple roles, role exists, member missing",
			bindings: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.admin",
					Members: []string{"user:admin@example.com"},
				},
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com"},
				},
			},
			role:   "roles/pubsub.publisher",
			member: "user:other@example.com",
			expected: []*pubsub.Binding{
				{
					Role:    "roles/pubsub.admin",
					Members: []string{"user:admin@example.com"},
				},
				{
					Role:    "roles/pubsub.publisher",
					Members: []string{"user:test@example.com", "user:other@example.com"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := mergeIAMBinding(tt.bindings, tt.role, tt.member)
			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("mergeIAMBinding() = %v, want %v", result, tt.expected)
			}
		})
	}
}
