package parametermanager

import (
	"strconv"
	"testing"
)

func BenchmarkGetFilteredParameters(b *testing.B) {
	s := &Service{}
	params := make([]Parameter, 100)
	for i := 0; i < 100; i++ {
		labels := make(map[string]string)
		for j := 0; j < 10; j++ {
			labels["key"+strconv.Itoa(j)] = "value" + strconv.Itoa(j)
		}
		params[i] = Parameter{
			Name:   "Param" + strconv.Itoa(i),
			Format: "text",
			Labels: labels,
		}
	}

	query := "value5"
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.getFilteredParameters(params, query)
	}
}
