package logging

import "testing"

// TestComposeFilter covers how the base (resource-scoping) filter, the
// user's free-text LQL query, and the severity threshold are ANDed together
// into the string actually sent to the API.
func TestComposeFilter(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		query    string
		severity string
		want     string
	}{
		{name: "all empty", base: "", query: "", severity: "", want: ""},
		{name: "base only", base: `resource.type="cloud_run_revision"`, query: "", severity: "", want: `resource.type="cloud_run_revision"`},
		{name: "query only", base: "", query: `textPayload:"timeout"`, severity: "", want: `(textPayload:"timeout")`},
		{name: "base and query", base: `resource.type="gce_instance"`, query: `textPayload:"oom"`, severity: "",
			want: `resource.type="gce_instance" AND (textPayload:"oom")`},
		{name: "base and severity", base: `resource.type="gce_instance"`, query: "", severity: "WARNING",
			want: `resource.type="gce_instance" AND severity>=WARNING`},
		{name: "all three", base: `resource.type="gce_instance"`, query: `textPayload:"oom"`, severity: "WARNING",
			want: `resource.type="gce_instance" AND (textPayload:"oom") AND severity>=WARNING`},
		{name: "DEFAULT severity is treated as no filter", base: "", query: "", severity: "DEFAULT", want: ""},
		{name: "query whitespace is trimmed but still wrapped", base: "", query: "  severity>=ERROR  ", severity: "",
			want: "(severity>=ERROR)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := composeFilter(tt.base, tt.query, tt.severity); got != tt.want {
				t.Errorf("composeFilter(%q, %q, %q) = %q, want %q", tt.base, tt.query, tt.severity, got, tt.want)
			}
		})
	}
}

// TestSeverityLevelLabels_ParallelToSeverityLevels guards the severity
// picker's display labels against drifting out of sync with SeverityLevels.
func TestSeverityLevelLabels_ParallelToSeverityLevels(t *testing.T) {
	if len(severityLevelLabels) != len(SeverityLevels) {
		t.Fatalf("len(severityLevelLabels) = %d, want %d (parallel to SeverityLevels)", len(severityLevelLabels), len(SeverityLevels))
	}
}

// TestLQLSuggestions_NoDuplicates guards the query autocomplete dictionary
// against accidental duplicate entries (bubbles/textinput.SetSuggestions
// doesn't dedupe, so a duplicate would just show up twice while cycling).
func TestLQLSuggestions_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	sugs := lqlSuggestions()
	if len(sugs) == 0 {
		t.Fatal("lqlSuggestions() returned no suggestions")
	}
	for _, s := range sugs {
		if seen[s] {
			t.Errorf("duplicate suggestion %q", s)
		}
		seen[s] = true
	}
}

// TestIsValidSeverity_MatchesSeverityLevels guards against SeverityLevels and
// isValidSeverity drifting apart if one is edited without the other.
func TestIsValidSeverity_MatchesSeverityLevels(t *testing.T) {
	for _, lvl := range SeverityLevels {
		if !isValidSeverity(lvl) {
			t.Errorf("isValidSeverity(%q) = false, want true (present in SeverityLevels)", lvl)
		}
	}
	if isValidSeverity("NOT_A_SEVERITY") {
		t.Error(`isValidSeverity("NOT_A_SEVERITY") = true, want false`)
	}
}
