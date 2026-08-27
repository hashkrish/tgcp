package disks

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// keyMsg builds a tea.KeyMsg the same way other packages' tests do, so
// Update() can be driven with real bubbletea messages instead of poking at
// unexported fields directly.
func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

func sampleDisks() []Disk {
	return []Disk{
		{Name: "web-1", Zone: "us-central1-a", SizeGb: 10, Type: "pd-ssd", Status: "READY", Users: []string{"instances/inst-1"}},
		{Name: "web-2", Zone: "us-east1-b", SizeGb: 20, Type: "pd-balanced", Status: "READY"},
		{Name: "db-data", Zone: "us-central1-a", SizeGb: 100, Type: "pd-ssd", Status: "READY"},
	}
}

// -----------------------------------------------------------------------------
// getFilteredDisks
// -----------------------------------------------------------------------------

func TestGetFilteredDisks(t *testing.T) {
	s := NewService(nil)
	disks := sampleDisks()

	tests := []struct {
		name  string
		query string
		want  []string // expected disk names, in order
	}{
		{"empty query returns all", "", []string{"web-1", "web-2", "db-data"}},
		{"matches by name substring", "web", []string{"web-1", "web-2"}},
		{"matches by zone", "us-east1-b", []string{"web-2"}},
		{"matches by type", "pd-ssd", []string{"web-1", "db-data"}},
		{"matches by status", "ready", []string{"web-1", "web-2", "db-data"}},
		{"case-insensitive", "WEB-1", []string{"web-1"}},
		{"no match", "does-not-exist", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.getFilteredDisks(disks, tt.query)
			if len(got) != len(tt.want) {
				t.Fatalf("getFilteredDisks(%q) = %v, want %v", tt.query, namesOf(got), tt.want)
			}
			for i, d := range got {
				if d.Name != tt.want[i] {
					t.Errorf("getFilteredDisks(%q)[%d] = %q, want %q", tt.query, i, d.Name, tt.want[i])
				}
			}
		})
	}
}

func namesOf(disks []Disk) []string {
	names := make([]string, len(disks))
	for i, d := range disks {
		names[i] = d.Name
	}
	return names
}

// -----------------------------------------------------------------------------
// Update(): cursor-bounds safety with empty/short disk lists
// -----------------------------------------------------------------------------

// TestUpdate_EmptyList_EnterIsSafe mirrors the class of stale-pointer/index
// bugs found earlier: pressing "enter" (or any action key) against an empty
// disk list must never panic and must never select a disk.
func TestUpdate_EmptyList_EnterIsSafe(t *testing.T) {
	s := NewService(nil)

	// Populate with zero disks, as a real fetch returning no results would.
	model, _ := s.Update(disksMsg{})
	s = model.(*Service)

	if len(s.disks) != 0 {
		t.Fatalf("expected 0 disks, got %d", len(s.disks))
	}

	model, _ = s.Update(keyMsg("enter"))
	s = model.(*Service)

	if s.selectedDisk != nil {
		t.Errorf("expected no disk selected on enter against an empty list, got %+v", s.selectedDisk)
	}
	if s.viewState != ViewList {
		t.Errorf("expected to remain in ViewList, got %v", s.viewState)
	}
}

// TestUpdate_ShortList_CursorClampsAndSelectsSafely guards the case where
// something tries to push the table's cursor past the current row count,
// e.g. a stale selection surviving a filter narrowing the list. Both
// bubbles/table's Model.SetCursor and StandardTable.SetRows already clamp
// the cursor to a valid index as soon as it would go out of range (verified
// against bubbles@v0.21.0's SetCursor: `clamp(n, 0, len(m.rows)-1)`), so
// there's no way to reach disks.go's enter handler with an actually
// out-of-range s.table.Cursor() through the table's public API — this test
// exists to pin that guarantee down and catch a regression if it ever
// changes, not to exercise disks.go's own (redundant but harmless) bounds
// check.
func TestUpdate_ShortList_CursorClampsAndSelectsSafely(t *testing.T) {
	s := NewService(nil)

	model, _ := s.Update(disksMsg(sampleDisks()[:1])) // single disk
	s = model.(*Service)

	// Attempt to push the cursor out of bounds; SetCursor clamps it back
	// to the last valid row (index 0) rather than storing 5 verbatim.
	s.table.SetCursor(5)
	if got := s.table.Cursor(); got != 0 {
		t.Fatalf("expected SetCursor(5) on a 1-row table to clamp to 0, got %d", got)
	}

	model, _ = s.Update(keyMsg("enter"))
	s = model.(*Service)

	if s.selectedDisk == nil || s.selectedDisk.Name != "web-1" {
		t.Errorf("expected the single disk to be selected after clamping, got %+v", s.selectedDisk)
	}
	if s.viewState != ViewDetail {
		t.Errorf("expected to enter ViewDetail after selecting the clamped row, got %v", s.viewState)
	}
}

// TestUpdate_ConfirmationView_NoSelectedDisk_ActionKeyIsSafe guards the
// snapshot-confirmation path (real mutating action) against a nil
// selectedDisk, which must never happen via normal navigation but must not
// panic or fire an action if it somehow does.
func TestUpdate_ConfirmationView_NoSelectedDisk_ActionKeyIsSafe(t *testing.T) {
	s := NewService(nil)
	s.viewState = ViewConfirmation
	s.pendingAction = "snapshot"
	s.actionSource = ViewDetail
	s.selectedDisk = nil

	model, cmd := s.Update(keyMsg("enter"))
	s = model.(*Service)

	if cmd != nil {
		t.Error("expected no command to be dispatched without a selected disk")
	}
	if s.viewState != ViewDetail {
		t.Errorf("expected viewState to return to actionSource ViewDetail, got %v", s.viewState)
	}
	if s.pendingAction != "" {
		t.Errorf("expected pendingAction to be cleared, got %q", s.pendingAction)
	}
}

// TestUpdate_DetailView_SnapshotKey_NoSelectedDisk verifies the "s" (snapshot)
// key in the detail view is guarded by a nil selectedDisk check and does not
// transition to confirmation without a disk to act on.
func TestUpdate_DetailView_SnapshotKey_NoSelectedDisk(t *testing.T) {
	s := NewService(nil)
	s.viewState = ViewDetail
	s.selectedDisk = nil

	model, _ := s.Update(keyMsg("s"))
	s = model.(*Service)

	if s.viewState != ViewDetail {
		t.Errorf("expected to remain in ViewDetail without a selected disk, got %v", s.viewState)
	}
	if s.pendingAction != "" {
		t.Errorf("expected pendingAction to remain unset, got %q", s.pendingAction)
	}
}

// -----------------------------------------------------------------------------
// ViewState transitions: List -> Detail -> Confirmation -> back
// -----------------------------------------------------------------------------

func TestUpdate_ViewStateTransitions_FullRoundTrip(t *testing.T) {
	s := NewService(nil)

	model, _ := s.Update(disksMsg(sampleDisks()))
	s = model.(*Service)
	s.table.SetCursor(0)

	// List -> Detail
	model, _ = s.Update(keyMsg("enter"))
	s = model.(*Service)
	if s.viewState != ViewDetail {
		t.Fatalf("expected ViewDetail after enter, got %v", s.viewState)
	}
	if s.selectedDisk == nil || s.selectedDisk.Name != "web-1" {
		t.Fatalf("expected selectedDisk web-1, got %+v", s.selectedDisk)
	}

	// Detail -> Confirmation (snapshot)
	model, _ = s.Update(keyMsg("s"))
	s = model.(*Service)
	if s.viewState != ViewConfirmation {
		t.Fatalf("expected ViewConfirmation after 's', got %v", s.viewState)
	}
	if s.pendingAction != "snapshot" {
		t.Fatalf("expected pendingAction 'snapshot', got %q", s.pendingAction)
	}
	if s.actionSource != ViewDetail {
		t.Fatalf("expected actionSource ViewDetail, got %v", s.actionSource)
	}

	// Confirmation -> back to Detail (cancel)
	model, _ = s.Update(keyMsg("n"))
	s = model.(*Service)
	if s.viewState != ViewDetail {
		t.Fatalf("expected to return to ViewDetail after cancel, got %v", s.viewState)
	}
	if s.pendingAction != "" {
		t.Errorf("expected pendingAction cleared after cancel, got %q", s.pendingAction)
	}
	// Cancelling must not clear the selected disk; we're still viewing it.
	if s.selectedDisk == nil {
		t.Fatal("expected selectedDisk to remain set after cancelling confirmation")
	}

	// Detail -> List
	model, _ = s.Update(keyMsg("esc"))
	s = model.(*Service)
	if s.viewState != ViewList {
		t.Fatalf("expected ViewList after esc from detail, got %v", s.viewState)
	}
	if s.selectedDisk != nil {
		t.Errorf("expected selectedDisk cleared after leaving detail view, got %+v", s.selectedDisk)
	}
}

func TestUpdate_ViewStateTransitions_ConfirmYieldsCommand(t *testing.T) {
	s := NewService(nil)

	model, _ := s.Update(disksMsg(sampleDisks()))
	s = model.(*Service)
	s.table.SetCursor(0)

	model, _ = s.Update(keyMsg("enter")) // -> Detail
	s = model.(*Service)
	model, _ = s.Update(keyMsg("s")) // -> Confirmation
	s = model.(*Service)

	model, cmd := s.Update(keyMsg("y")) // confirm
	s = model.(*Service)

	if s.viewState != ViewDetail {
		t.Fatalf("expected to return to ViewDetail after confirming, got %v", s.viewState)
	}
	if s.pendingAction != "" {
		t.Errorf("expected pendingAction cleared after confirming, got %q", s.pendingAction)
	}
	if cmd == nil {
		t.Error("expected a command to be dispatched on confirm (CreateSnapshotCmd), got nil")
	}
}

// -----------------------------------------------------------------------------
// Reset()
// -----------------------------------------------------------------------------

func TestReset_ClearsSelectionErrorAndCursor(t *testing.T) {
	s := NewService(nil)

	model, _ := s.Update(disksMsg(sampleDisks()))
	s = model.(*Service)
	s.table.SetCursor(2)

	model, _ = s.Update(keyMsg("enter"))
	s = model.(*Service)
	if s.selectedDisk == nil {
		t.Fatal("setup: expected a selected disk before Reset")
	}

	model, _ = s.Update(errMsg(errBoom))
	s = model.(*Service)
	if s.err == nil {
		t.Fatal("setup: expected err to be set before Reset")
	}

	s.Reset()

	if s.viewState != ViewList {
		t.Errorf("expected viewState ViewList after Reset, got %v", s.viewState)
	}
	if s.selectedDisk != nil {
		t.Errorf("expected selectedDisk nil after Reset, got %+v", s.selectedDisk)
	}
	if s.err != nil {
		t.Errorf("expected err nil after Reset, got %v", s.err)
	}
	if s.table.Cursor() != 0 {
		t.Errorf("expected table cursor reset to 0, got %d", s.table.Cursor())
	}
}

var errBoom = testErr("boom")

type testErr string

func (e testErr) Error() string { return string(e) }
