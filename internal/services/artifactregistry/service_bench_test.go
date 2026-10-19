package artifactregistry

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkCleanupPolicy_Concat(b *testing.B) {
	count := int32(5)
	dryRun := true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		cleanup := fmt.Sprintf("%d policies", count)
		if dryRun {
			cleanup += " (dry run)"
		}
		_ = cleanup
	}
}

func BenchmarkCleanupPolicy_BuilderStrconv(b *testing.B) {
	count := int32(5)
	dryRun := true
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var builder strings.Builder
		// Capacity hint: 1 (digit) + 9 (" policies") + 10 (" (dry run)") = 20
		builder.Grow(32)
		builder.WriteString(strconv.Itoa(int(count)))
		builder.WriteString(" policies")
		if dryRun {
			builder.WriteString(" (dry run)")
		}
		_ = builder.String()
	}
}
