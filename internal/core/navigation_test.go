package core

import (
	"strings"
	"testing"
)

// fixtureCommands returns a small, representative command set for testing
// FilterCommands' ranking algorithm in isolation from whatever the real
// production command list (built from the live service registry, see
// serviceCommands in internal/ui/model.go) happens to contain.
func fixtureCommands() []Command {
	noop := func() Route { return Route{} }
	return []Command{
		{Name: "GCE: List Instances", Description: "List Google Compute Engine VM instances", Action: noop},
		{Name: "GKE: List Clusters", Description: "List Kubernetes Engine Clusters", Action: noop},
		{Name: "GCS: List Buckets", Description: "List Cloud Storage Buckets", Action: noop},
		{Name: "Dataproc: List Clusters", Description: "List Dataproc Clusters", Action: noop},
	}
}

// TestFilterCommands_PrefixBeatsFuzzy is the regression test for the
// "gce" → GKE-ranked-first bug. The ranker must prefer literal prefix
// matches on Name over a fuzzy hit spread across Name + Description.
func TestFilterCommands_PrefixBeatsFuzzy(t *testing.T) {
	m := NewNavigation()
	m.SetCommands(fixtureCommands())
	m.FilterCommands("gce")

	if len(m.Suggestions) == 0 {
		t.Fatal("expected at least one suggestion for 'gce'")
	}
	first := m.Suggestions[0].Name
	if !strings.HasPrefix(strings.ToLower(first), "gce") {
		t.Fatalf("expected first result to start with 'gce', got %q", first)
	}
}

func TestFilterCommands_SubstringBeatsFuzzy(t *testing.T) {
	// "cluster" is not a prefix of any command; it is a literal substring
	// of "GKE: List Clusters" and "Dataproc: List Clusters". Those should
	// outrank any fuzzy match that only shares individual characters.
	m := NewNavigation()
	m.SetCommands(fixtureCommands())
	m.FilterCommands("cluster")

	if len(m.Suggestions) < 2 {
		t.Fatalf("expected ≥2 suggestions for 'cluster', got %d", len(m.Suggestions))
	}
	for i, s := range m.Suggestions[:2] {
		if !strings.Contains(strings.ToLower(s.Name), "cluster") {
			t.Errorf("suggestion[%d] %q missing literal 'cluster' substring", i, s.Name)
		}
	}
}

// TestFilterCommands_EmptyQueryShowsFullList is a regression test: an empty
// query must show every available command, not just recently-executed ones
// (or nothing at all). Showing only recents made most services look like
// they'd vanished from the palette the moment it was opened without typing
// anything -- the fix is that recency only reorders the top of the list,
// it never hides commands from it.
func TestFilterCommands_EmptyQueryShowsFullList(t *testing.T) {
	m := NewNavigation()
	cmds := fixtureCommands()
	m.SetCommands(cmds)

	// No history yet: empty query must still show every command.
	m.FilterCommands("")
	if len(m.Suggestions) != len(cmds) {
		t.Fatalf("expected all %d commands with no history, got %d", len(cmds), len(m.Suggestions))
	}

	// Execute one command, then re-open with an empty query: it must be
	// pinned to the top, but every other command must still be present.
	m.FilterCommands("gce")
	m.Selection = 0
	m.ExecuteSelection()
	m.FilterCommands("")

	if len(m.Suggestions) != len(cmds) {
		t.Fatalf("expected all %d commands after recording history, got %d", len(cmds), len(m.Suggestions))
	}
	if got := m.Suggestions[0].Name; !strings.HasPrefix(got, "GCE:") {
		t.Errorf("expected the just-executed GCE command pinned first, got %q", got)
	}
	seen := make(map[string]bool)
	for _, s := range m.Suggestions {
		seen[s.Name] = true
	}
	for _, c := range cmds {
		if !seen[c.Name] {
			t.Errorf("command %q missing from the empty-query suggestion list", c.Name)
		}
	}
}

// TestFilterCommands_SubstringMatchesDescriptionToo is a regression test:
// tier 2 (literal substring) previously only searched Name, so a keyword
// that only appears in Description (e.g. a service's sub-resources, see
// serviceSearchAliases in internal/ui/model.go) fell through to the weaker
// fuzzy tier, or didn't match at all if fuzzy's subsequence matching missed
// it. It must now be found as a literal substring match.
func TestFilterCommands_SubstringMatchesDescriptionToo(t *testing.T) {
	m := NewNavigation()
	m.SetCommands([]Command{
		{Name: "VPC Network", Description: "Open VPC Network (net) — subnets, firewall rules", Action: func() Route { return Route{} }},
		{Name: "Other Service", Description: "Unrelated", Action: func() Route { return Route{} }},
	})
	m.FilterCommands("firewall")

	if len(m.Suggestions) != 1 {
		t.Fatalf("expected exactly 1 match for 'firewall', got %d", len(m.Suggestions))
	}
	if got := m.Suggestions[0].Name; got != "VPC Network" {
		t.Errorf("expected 'VPC Network' matched via its Description, got %q", got)
	}
}

func TestFilterCommands_NoMatch(t *testing.T) {
	m := NewNavigation()
	m.SetCommands(fixtureCommands())
	m.FilterCommands("zzzz-definitely-no-match-zzzz")
	if len(m.Suggestions) != 0 {
		t.Errorf("expected 0 suggestions for impossible query, got %d", len(m.Suggestions))
	}
}

func TestFilterCommands_MatchedIndexesCoverQuery(t *testing.T) {
	// For prefix and substring tiers, MatchedIndexes must cover the entire
	// query span so the palette highlights the whole match, not just the
	// first char.
	m := NewNavigation()
	m.SetCommands(fixtureCommands())
	m.FilterCommands("gcs")
	if len(m.Suggestions) == 0 {
		t.Fatal("expected matches for 'gcs'")
	}
	first := m.Suggestions[0]
	if len(first.MatchedIndexes) != 3 {
		t.Errorf("expected 3 highlighted chars for 'gcs', got %d", len(first.MatchedIndexes))
	}
}
