package gke

import (
	"testing"
)

func TestSanitizeContextName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid context name",
			input:    "gke_my-project_us-central1-a_cluster-1",
			expected: "gke_my-project_us-central1-a_cluster-1",
		},
		{
			name:     "context name with invalid characters",
			input:    "gke_$(touch /tmp/pwned)_zone_name",
			expected: "gke_touchtmppwned_zone_name",
		},
		{
			name:     "context name with colon",
			input:    "gke:my-project:us-central1-a:cluster-1",
			expected: "gke:my-project:us-central1-a:cluster-1",
		},
		{
			name:     "context name with special characters",
			input:    "gke_my-project_us-central1-a_cluster-1!@#$%^&*()_+{}|\"<>?",
			expected: "gke_my-project_us-central1-a_cluster-1_",
		},
		{
			name:     "context name with space",
			input:    "gke my-project",
			expected: "gkemy-project",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeContextName(tt.input); got != tt.expected {
				t.Errorf("sanitizeContextName() = %v, want %v", got, tt.expected)
			}
		})
	}
}
