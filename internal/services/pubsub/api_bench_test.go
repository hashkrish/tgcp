package pubsub

import (
	"slices"
	"testing"
	"google.golang.org/api/pubsub/v1"
)

// Benchmark without pre-allocation
func BenchmarkAppendWithoutPrealloc(b *testing.B) {
	page := &pubsub.ListTopicsResponse{
		Topics: make([]*pubsub.Topic, 1000),
	}
	for i := 0; i < 1000; i++ {
		page.Topics[i] = &pubsub.Topic{
			Name: "projects/test/topics/test-topic",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var topics []Topic
		for _, t := range page.Topics {
			topics = append(topics, Topic{
				Name:      shortName(t.Name),
				ProjectID: "test",
			})
		}
		_ = topics
	}
}

// Benchmark with slices.Grow pre-allocation
func BenchmarkAppendWithGrow(b *testing.B) {
	page := &pubsub.ListTopicsResponse{
		Topics: make([]*pubsub.Topic, 1000),
	}
	for i := 0; i < 1000; i++ {
		page.Topics[i] = &pubsub.Topic{
			Name: "projects/test/topics/test-topic",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var topics []Topic
		topics = slices.Grow(topics, len(page.Topics))
		for _, t := range page.Topics {
			topics = append(topics, Topic{
				Name:      shortName(t.Name),
				ProjectID: "test",
			})
		}
		_ = topics
	}
}
