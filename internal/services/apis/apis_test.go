package apis

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/demo"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// run executes cmd and feeds the resulting message back into the service,
// descending into batches. Ticks and spinner animation are skipped.
func run(t *testing.T, s *Service, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			run(t, s, c)
		}
	case apisMsg, actionResultMsg, errMsg:
		_, next := s.Update(msg)
		run(t, s, next)
	}
}

func newDemoService(t *testing.T) *Service {
	t.Helper()
	// Actions record to job history under ~/.tgcp; keep that out of the
	// real home directory.
	t.Setenv("HOME", t.TempDir())
	demo.Enabled = true
	t.Cleanup(func() { demo.Enabled = false })
	s := NewService(nil)
	if err := s.InitService(context.Background(), demo.FixtureProject); err != nil {
		t.Fatal(err)
	}
	run(t, s, s.fetchCmd(tabEnabled, true))
	return s
}

func TestEnabledTabListsOnlyEnabledAPIs(t *testing.T) {
	s := newDemoService(t)
	if len(s.enabled) == 0 {
		t.Fatal("expected enabled APIs from fixture")
	}
	for _, a := range s.enabled {
		if a.State != "ENABLED" {
			t.Errorf("%s has state %s on Enabled tab", a.Name, a.State)
		}
	}
}

func TestSwitchTabLoadsCommonAPIs(t *testing.T) {
	s := newDemoService(t)
	cmd, ok := s.NextTab()
	if !ok || s.activeTab != tabCommon {
		t.Fatal("expected switch to Common tab")
	}
	run(t, s, cmd)
	if !s.loaded[tabCommon] || len(s.common) <= len(s.enabled) {
		t.Fatalf("expected common list to include disabled APIs, got common=%d enabled=%d", len(s.common), len(s.enabled))
	}
}

func TestCommonAPIsHasNoDuplicates(t *testing.T) {
	seen := map[string]bool{}
	for _, n := range commonAPIs {
		if seen[n] {
			t.Errorf("duplicate entry %q", n)
		}
		seen[n] = true
	}
}

func TestDisableRequiresConfirmation(t *testing.T) {
	s := newDemoService(t)
	target := s.cursorAPI()
	if target == nil {
		t.Fatal("no API under cursor")
	}

	s.Update(key("x"))
	if s.viewState != ViewConfirmation || s.pendingAction != "disable" || s.pendingName != target.Name {
		t.Fatalf("expected disable confirmation for %s, got view=%d action=%q name=%q",
			target.Name, s.viewState, s.pendingAction, s.pendingName)
	}

	s.Update(key("n"))
	if s.viewState != ViewList || s.pendingAction != "" {
		t.Fatal("cancel should return to list and clear pending action")
	}
}

func TestEnableOnlyOffersForDisabledAPIs(t *testing.T) {
	s := newDemoService(t)
	// Everything on the Enabled tab is already enabled, so "e" is a no-op.
	s.Update(key("e"))
	if s.viewState != ViewList {
		t.Fatal("enable should not be offered for an already-enabled API")
	}
}

func TestEnableByNameFlow(t *testing.T) {
	s := newDemoService(t)
	s.Update(key("n"))
	if s.viewState != ViewEnableForm {
		t.Fatal("expected enable form")
	}
	s.Update(key("spanner.googleapis.com"))
	s.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if s.viewState != ViewConfirmation || s.pendingName != "spanner.googleapis.com" {
		t.Fatalf("expected confirmation for typed name, got view=%d name=%q", s.viewState, s.pendingName)
	}
	_, cmd := s.Update(key("y"))
	if s.viewState != ViewList {
		t.Fatal("expected return to list after confirm")
	}
	run(t, s, cmd)
}
