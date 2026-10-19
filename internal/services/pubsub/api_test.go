package pubsub

import (
	"context"
	"testing"

	"github.com/yogirk/tgcp/internal/demo"
)

func TestNewClient(t *testing.T) {
	// Save the original value and restore it after the test
	originalDemoEnabled := demo.Enabled
	t.Cleanup(func() {
		demo.Enabled = originalDemoEnabled
	})

	demo.Enabled = true

	client, err := NewClient(context.Background())
	if err != nil {
		t.Fatalf("NewClient() expected no error, got %v", err)
	}

	if client == nil {
		t.Fatalf("NewClient() expected a non-nil client, got nil")
	}
}
