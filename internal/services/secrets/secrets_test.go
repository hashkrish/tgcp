package secrets

import (
	"fmt"
	"testing"
)

func BenchmarkGetFilteredSecrets(b *testing.B) {
	// Create some dummy secrets
	secrets := make([]Secret, 1000)
	for i := 0; i < 1000; i++ {
		secrets[i] = Secret{
			Name:        fmt.Sprintf("secret-%d", i),
			Replication: "automatic",
			Labels: map[string]string{
				"env":  "production",
				"app":  fmt.Sprintf("app-%d", i),
				"team": "infrastructure",
				"tier": "backend",
			},
		}
	}

	svc := &Service{}
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		svc.getFilteredSecrets(secrets, "app-500")
	}
}
