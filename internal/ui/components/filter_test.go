package components

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// containsCI is the matcher used across these tests: a case-insensitive
// substring match, mirroring how services typically wire FilterSlice.
func containsCI(item string, query string) bool {
	return strings.Contains(strings.ToLower(item), query)
}

func filterItems(items []string, query string) []string {
	return FilterSlice(items, query, containsCI)
}

func keyMsg(runes string) tea.KeyMsg {
	if len(runes) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(runes)}
	}
	switch runes {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "/":
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(runes)}
}

func TestFilterSlice(t *testing.T) {
	items := []string{"Compute Engine", "Kubernetes Engine", "Cloud Storage", "Cloud SQL"}

	tests := []struct {
		name  string
		query string
		want  []string
	}{
		{"empty query returns all", "", items},
		{"case-insensitive substring", "cloud", []string{"Cloud Storage", "Cloud SQL"}},
		{"matches nothing", "zzz-nope", nil},
		{"matches single item", "kubernetes", []string{"Kubernetes Engine"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterItems(items, tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("filterItems(%q) = %v, want %v", tt.query, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("filterItems(%q)[%d] = %q, want %q", tt.query, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestFilterModel_EnterExitFilterMode(t *testing.T) {
	m := NewFilter()
	if m.IsActive() {
		t.Fatal("new filter should not be active")
	}

	m.EnterFilterMode()
	if !m.IsActive() {
		t.Fatal("expected filter to be active after EnterFilterMode")
	}
	if !m.TextInput.Focused() {
		t.Error("expected text input to be focused after EnterFilterMode")
	}

	m.TextInput.SetValue("gce")
	m.ExitFilterMode()
	if m.IsActive() {
		t.Error("expected filter to be inactive after ExitFilterMode")
	}
	if m.Value() != "" {
		t.Errorf("ExitFilterMode should reset the value, got %q", m.Value())
	}
}

func TestFilterModel_ExitFilterModeKeepValue(t *testing.T) {
	m := NewFilter()
	m.EnterFilterMode()
	m.TextInput.SetValue("gce")

	m.ExitFilterModeKeepValue()
	if m.IsActive() {
		t.Error("expected filter to be inactive after ExitFilterModeKeepValue")
	}
	if m.Value() != "gce" {
		t.Errorf("ExitFilterModeKeepValue should keep the value, got %q", m.Value())
	}
}

func TestFilterModel_HandleKeyMsg(t *testing.T) {
	t.Run("slash enters filter mode", func(t *testing.T) {
		m := NewFilter()
		shouldExit, shouldKeepValue, cmd := m.HandleKeyMsg(keyMsg("/"))
		if shouldExit {
			t.Error("entering filter mode should not report shouldExit")
		}
		if shouldKeepValue {
			t.Error("entering filter mode should not report shouldKeepValue")
		}
		if cmd == nil {
			t.Error("expected a focus command when entering filter mode")
		}
		if !m.IsActive() {
			t.Error("expected filter to become active on '/'")
		}
	})

	t.Run("esc while active exits and clears", func(t *testing.T) {
		m := NewFilter()
		m.EnterFilterMode()
		m.TextInput.SetValue("gce")

		shouldExit, shouldKeepValue, _ := m.HandleKeyMsg(keyMsg("esc"))
		if !shouldExit {
			t.Error("esc while active should exit filter mode")
		}
		if shouldKeepValue {
			t.Error("esc should not keep the value")
		}
		if m.Value() != "" {
			t.Errorf("esc should clear the value, got %q", m.Value())
		}
	})

	t.Run("enter while active exits and keeps value", func(t *testing.T) {
		m := NewFilter()
		m.EnterFilterMode()
		m.TextInput.SetValue("gce")

		shouldExit, shouldKeepValue, _ := m.HandleKeyMsg(keyMsg("enter"))
		if !shouldExit {
			t.Error("enter while active should exit filter mode")
		}
		if !shouldKeepValue {
			t.Error("enter should keep the value")
		}
		if m.Value() != "gce" {
			t.Errorf("enter should keep the value, got %q", m.Value())
		}
	})

	t.Run("esc while inactive with a value clears without entering", func(t *testing.T) {
		m := NewFilter()
		m.TextInput.SetValue("gce") // simulate an applied-but-inactive filter

		shouldExit, shouldKeepValue, cmd := m.HandleKeyMsg(keyMsg("esc"))
		if !shouldExit {
			t.Error("esc on an applied filter should report shouldExit so caller clears state")
		}
		if shouldKeepValue {
			t.Error("esc should not keep the value")
		}
		if cmd != nil {
			t.Error("esc-to-clear should not return a command")
		}
		if m.Value() != "" {
			t.Errorf("esc should reset the value, got %q", m.Value())
		}
	})

	t.Run("printable key while active updates input", func(t *testing.T) {
		m := NewFilter()
		m.EnterFilterMode()

		shouldExit, _, _ := m.HandleKeyMsg(keyMsg("g"))
		if shouldExit {
			t.Error("typing while active should not exit filter mode")
		}
		if m.Value() != "g" {
			t.Errorf("expected value %q, got %q", "g", m.Value())
		}
	})

	t.Run("key while inactive and no existing value is a no-op", func(t *testing.T) {
		m := NewFilter()
		shouldExit, shouldKeepValue, cmd := m.HandleKeyMsg(keyMsg("g"))
		if shouldExit || shouldKeepValue || cmd != nil {
			t.Errorf("expected no-op, got shouldExit=%v shouldKeepValue=%v cmd=%v", shouldExit, shouldKeepValue, cmd)
		}
		if m.IsActive() {
			t.Error("filter should remain inactive")
		}
	})
}

func TestFilterModel_SetMatchCounts(t *testing.T) {
	m := NewFilter()
	m.SetMatchCounts(10, 3)
	if m.Total != 10 || m.Matches != 3 {
		t.Errorf("got Total=%d Matches=%d, want Total=10 Matches=3", m.Total, m.Matches)
	}
}

func TestIsNavigationKey(t *testing.T) {
	navKeys := []string{"up", "down", "j", "k", "g", "G", "home", "end", "pageup", "pagedown"}
	for _, k := range navKeys {
		if !IsNavigationKey(k) {
			t.Errorf("expected %q to be a navigation key", k)
		}
	}

	nonNavKeys := []string{"a", "/", "enter", "esc", "1", ""}
	for _, k := range nonNavKeys {
		if IsNavigationKey(k) {
			t.Errorf("expected %q to not be a navigation key", k)
		}
	}
}

// --- HandleFilterUpdate / FilterSession ---------------------------------

func newFilterHarness() (filter *FilterModel, all []string, table *[]string) {
	f := NewFilter()
	all = []string{"Compute Engine", "Kubernetes Engine", "Cloud Storage", "Cloud SQL"}
	visible := []string{}
	table = &visible
	return &f, all, table
}

func TestHandleFilterUpdate_EntersFilterMode(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	result := HandleFilterUpdate(filter, keyMsg("/"), all, filterItems, updateTable)
	if !result.Handled {
		t.Error("expected '/' to be handled")
	}
	if result.ShouldContinue {
		t.Error("entering filter mode should not continue to service handling")
	}
	if !filter.IsActive() {
		t.Error("expected filter to become active")
	}
}

func TestHandleFilterUpdate_TypingFiltersTableAndCounts(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	HandleFilterUpdate(filter, keyMsg("/"), all, filterItems, updateTable)
	HandleFilterUpdate(filter, keyMsg("c"), all, filterItems, updateTable)
	result := HandleFilterUpdate(filter, keyMsg("l"), all, filterItems, updateTable)

	if !result.Handled {
		t.Fatal("expected typing to be handled while filter is active")
	}
	want := []string{"Cloud Storage", "Cloud SQL"}
	if len(*visible) != len(want) {
		t.Fatalf("visible = %v, want %v", *visible, want)
	}
	for i := range want {
		if (*visible)[i] != want[i] {
			t.Errorf("visible[%d] = %q, want %q", i, (*visible)[i], want[i])
		}
	}
	if filter.Total != len(all) {
		t.Errorf("Total = %d, want %d", filter.Total, len(all))
	}
	if filter.Matches != len(want) {
		t.Errorf("Matches = %d, want %d", filter.Matches, len(want))
	}
}

func TestHandleFilterUpdate_EscClearsFilterAndRestoresFullTable(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	HandleFilterUpdate(filter, keyMsg("/"), all, filterItems, updateTable)
	HandleFilterUpdate(filter, keyMsg("c"), all, filterItems, updateTable)
	HandleFilterUpdate(filter, keyMsg("esc"), all, filterItems, updateTable)

	if filter.IsActive() {
		t.Error("expected filter to be inactive after esc")
	}
	if filter.Value() != "" {
		t.Errorf("expected value cleared after esc, got %q", filter.Value())
	}
	if len(*visible) != len(all) {
		t.Errorf("expected table restored to all %d items after clearing, got %d", len(all), len(*visible))
	}
	if filter.Matches != len(all) || filter.Total != len(all) {
		t.Errorf("expected Matches=Total=%d after clearing, got Matches=%d Total=%d", len(all), filter.Matches, filter.Total)
	}
}

func TestHandleFilterUpdate_EnterKeepsValueAndAppliedFilter(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	HandleFilterUpdate(filter, keyMsg("/"), all, filterItems, updateTable)
	HandleFilterUpdate(filter, keyMsg("c"), all, filterItems, updateTable)
	HandleFilterUpdate(filter, keyMsg("enter"), all, filterItems, updateTable)

	if filter.IsActive() {
		t.Error("expected filter to be inactive after enter")
	}
	if filter.Value() != "c" {
		t.Errorf("expected value kept after enter, got %q", filter.Value())
	}
	want := []string{"Compute Engine", "Cloud Storage", "Cloud SQL"}
	if len(*visible) != len(want) {
		t.Fatalf("visible = %v, want %v", *visible, want)
	}
}

func TestHandleFilterUpdate_NavigationKeyPassesThroughWhileActive(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	HandleFilterUpdate(filter, keyMsg("/"), all, filterItems, updateTable)
	result := HandleFilterUpdate(filter, keyMsg("down"), all, filterItems, updateTable)

	if result.Handled {
		t.Error("navigation keys should not be marked as handled by the filter")
	}
	if !result.ShouldContinue {
		t.Error("navigation keys should continue to service handling (e.g. table cursor movement)")
	}
	if !filter.IsActive() {
		t.Error("navigation keys should not exit filter mode")
	}
}

func TestHandleFilterUpdate_UnhandledKeyWhenInactive(t *testing.T) {
	filter, all, visible := newFilterHarness()
	updateTable := func(items []string) { *visible = items }

	result := HandleFilterUpdate(filter, keyMsg("a"), all, filterItems, updateTable)
	if result.Handled {
		t.Error("expected unhandled result when filter is inactive and key is not '/'")
	}
	if !result.ShouldContinue {
		t.Error("expected ShouldContinue when filter did not handle the key")
	}
}

// --- FilterSession -------------------------------------------------------

func TestFilterSession_Apply(t *testing.T) {
	f := NewFilter()
	var visible []string
	session := NewFilterSession(&f, filterItems, func(items []string) { visible = items })

	all := []string{"Compute Engine", "Kubernetes Engine", "Cloud Storage"}
	session.Apply(all)

	if len(visible) != len(all) {
		t.Fatalf("expected all items visible with no filter query, got %v", visible)
	}
	if f.Total != len(all) || f.Matches != len(all) {
		t.Errorf("expected Total=Matches=%d, got Total=%d Matches=%d", len(all), f.Total, f.Matches)
	}

	f.TextInput.SetValue("cloud")
	session.Apply(all)
	if len(visible) != 1 || visible[0] != "Cloud Storage" {
		t.Errorf("expected only Cloud Storage to match 'cloud', got %v", visible)
	}
	if f.Matches != 1 {
		t.Errorf("expected Matches=1, got %d", f.Matches)
	}
}

func TestFilterSession_HandleKey(t *testing.T) {
	f := NewFilter()
	var visible []string
	session := NewFilterSession(&f, filterItems, func(items []string) { visible = items })

	all := []string{"Compute Engine", "Kubernetes Engine", "Cloud Storage"}
	session.Apply(all)

	session.HandleKey(keyMsg("/"))
	result := session.HandleKey(keyMsg("b")) // 'b' is a plain printable, not a reserved nav key

	if !result.Handled {
		t.Error("expected HandleKey to report Handled while filter is active and typing")
	}
	if len(visible) != 1 || visible[0] != "Kubernetes Engine" {
		t.Errorf("expected only Kubernetes Engine to match 'b', got %v", visible)
	}
	if f.Matches != 1 || f.Total != len(all) {
		t.Errorf("expected Matches=1 Total=%d, got Matches=%d Total=%d", len(all), f.Matches, f.Total)
	}
}

func TestFilterSession_HandleKey_NilFilter(t *testing.T) {
	session := NewFilterSession[string](nil, filterItems, func([]string) {})
	result := session.HandleKey(keyMsg("/"))
	if result.Handled {
		t.Error("a session with a nil filter should never report Handled")
	}
	if !result.ShouldContinue {
		t.Error("a session with a nil filter should always continue")
	}
}
