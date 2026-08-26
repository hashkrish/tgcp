package components

import (
	"testing"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
)

func testColumns() []table.Column {
	return []table.Column{
		{Title: "Name", Width: 20},
		{Title: "Status", Width: 10},
	}
}

func testRows(n int) []table.Row {
	rows := make([]table.Row, n)
	for i := range rows {
		rows[i] = table.Row{"item", "OK"}
	}
	return rows
}

func TestNewStandardTable_Defaults(t *testing.T) {
	st := NewStandardTable(testColumns())
	if !st.focused {
		t.Error("expected table to be focused by default")
	}
	if st.heightOffset != 6 {
		t.Errorf("expected default heightOffset 6, got %d", st.heightOffset)
	}
}

func TestNewStandardTable_WithOptions(t *testing.T) {
	st := NewStandardTable(testColumns(),
		WithHeight(3),
		WithHeightOffset(9),
		WithFocused(false),
	)
	if st.focused {
		t.Error("expected WithFocused(false) to leave the table blurred")
	}
	if st.heightOffset != 9 {
		t.Errorf("expected heightOffset 9, got %d", st.heightOffset)
	}
}

func TestStandardTable_FocusBlur(t *testing.T) {
	st := NewStandardTable(testColumns())

	st.Blur()
	if st.focused {
		t.Error("expected focused=false after Blur")
	}

	st.Focus()
	if !st.focused {
		t.Error("expected focused=true after Focus")
	}
}

func TestStandardTable_SetRows_CursorInBounds(t *testing.T) {
	st := NewStandardTable(testColumns())
	st.SetRows(testRows(5))
	st.SetCursor(3)

	// Shrinking the row set so the previous cursor position is now
	// out-of-bounds must reset the cursor rather than leave it dangling
	// past the end of the table (this is exactly the class of stale
	// cursor/pointer bug this suite exists to catch).
	st.SetRows(testRows(2))
	if c := st.Cursor(); c < 0 || c >= 2 {
		t.Errorf("expected cursor to be clamped into [0,2), got %d", c)
	}
}

func TestStandardTable_SetRows_CursorAlreadyInBoundsIsUnchanged(t *testing.T) {
	st := NewStandardTable(testColumns())
	st.SetRows(testRows(5))
	st.SetCursor(2)

	st.SetRows(testRows(5))
	if c := st.Cursor(); c != 2 {
		t.Errorf("expected cursor to remain at 2 when still in bounds, got %d", c)
	}
}

func TestStandardTable_SetRows_Empty(t *testing.T) {
	st := NewStandardTable(testColumns())
	st.SetRows(testRows(5))
	st.SetCursor(3)

	// Setting an empty row set should not panic and should leave the table
	// usable; StandardTable.SetRows explicitly returns early for len(rows)==0
	// without touching the cursor.
	st.SetRows(testRows(0))
	if got := len(st.Rows()); got != 0 {
		t.Errorf("expected 0 rows, got %d", got)
	}
}

// Note: bubbles' table.Model.SetHeight(h) stores a viewport height of
// h minus the rendered header row, so Height() reports one less than the
// value StandardTable.SetHeight was called with. These expectations bake
// that in rather than re-deriving it, since it's the underlying library's
// contract, not something StandardTable controls.
func TestStandardTable_HandleWindowSize_MinimumHeight(t *testing.T) {
	st := NewStandardTable(testColumns(), WithHeightOffset(6))

	tests := []struct {
		name       string
		msgHeight  int
		offset     int
		wantHeight int
	}{
		{"generous space", 30, 6, 23},
		{"clamped to minimum", 8, 6, 4}, // 8-6=2, below the 5-row floor -> clamped to 5
		{"exact minimum boundary", 11, 6, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st.HandleWindowSize(tea.WindowSizeMsg{Height: tt.msgHeight}, tt.offset)
			if got := st.Height(); got != tt.wantHeight {
				t.Errorf("Height() = %d, want %d", got, tt.wantHeight)
			}
		})
	}
}

func TestStandardTable_HandleWindowSizeDefault_UsesStoredOffset(t *testing.T) {
	st := NewStandardTable(testColumns(), WithHeightOffset(10))
	st.HandleWindowSizeDefault(tea.WindowSizeMsg{Height: 30})
	if got := st.Height(); got != 19 {
		t.Errorf("Height() = %d, want %d", got, 19)
	}
}

func TestStandardTable_Update_ReturnsSameInstance(t *testing.T) {
	st := NewStandardTable(testColumns())
	st.SetRows(testRows(3))

	updated, _ := st.Update(tea.KeyMsg{Type: tea.KeyDown})
	if updated != st {
		t.Error("expected Update to return the same *StandardTable receiver")
	}
}

func TestStandardTable_View_DoesNotPanic(t *testing.T) {
	st := NewStandardTable(testColumns())
	st.SetRows(testRows(3))
	if v := st.View(); v == "" {
		t.Error("expected non-empty view output")
	}
}
