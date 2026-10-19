package secrets

import (
	"strings"
	"testing"
)

func BenchmarkLabelConcat(b *testing.B) {
	labels := map[string]string{
		"env":     "production",
		"app":     "frontend",
		"version": "1.0.0",
		"region":  "us-east-1",
		"tier":    "web",
	}

	b.Run("StringConcat", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var labelStr string
			for k, v := range labels {
				labelStr += k + "=" + v + " "
			}
			_ = labelStr
		}
	})

	b.Run("StringBuilder", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			var labelBuilder strings.Builder
			for k, v := range labels {
				labelBuilder.WriteString(k)
				labelBuilder.WriteString("=")
				labelBuilder.WriteString(v)
				labelBuilder.WriteString(" ")
			}
			_ = labelBuilder.String()
		}
	})
}
