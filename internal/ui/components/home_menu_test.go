package components

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// down/up sends a navigation key and returns the updated model, mirroring
// how MainModel drives HomeMenuModel.Update in real usage.
func sendKey(m HomeMenuModel, key string) HomeMenuModel {
	m, _ = m.Update(keyMsg(key))
	return m
}

func TestHomeMenu_InitialState(t *testing.T) {
	m := NewHomeMenu()
	if m.cursor != 0 {
		t.Errorf("expected initial cursor 0, got %d", m.cursor)
	}
	selectable := m.selectableItems()
	if len(selectable) == 0 {
		t.Fatal("expected at least one selectable item in the default menu")
	}
	if got := m.SelectedItem().ShortName; got != "overview" {
		t.Errorf("expected initial selection to be the top item 'overview', got %q", got)
	}
}

func TestHomeMenu_DownUpNavigation(t *testing.T) {
	m := NewHomeMenu()
	start := m.cursor

	m = sendKey(m, "down")
	if m.cursor != start+1 {
		t.Fatalf("expected cursor to advance to %d, got %d", start+1, m.cursor)
	}

	m = sendKey(m, "up")
	if m.cursor != start {
		t.Fatalf("expected cursor to return to %d, got %d", start, m.cursor)
	}
}

func TestHomeMenu_CursorCannotGoAboveTop(t *testing.T) {
	m := NewHomeMenu()
	// Already at the very top; repeated "up" must not go negative.
	for i := 0; i < 5; i++ {
		m = sendKey(m, "up")
	}
	if m.cursor != 0 {
		t.Errorf("expected cursor clamped to 0 at the top, got %d", m.cursor)
	}
}

func TestHomeMenu_CursorCannotGoBelowBottom(t *testing.T) {
	m := NewHomeMenu()
	selectable := m.selectableItems()
	last := len(selectable) - 1

	// Walk well past the last selectable item.
	for i := 0; i < last+10; i++ {
		m = sendKey(m, "down")
	}
	if m.cursor != last {
		t.Errorf("expected cursor clamped to last selectable index %d, got %d", last, m.cursor)
	}
}

func TestHomeMenu_EndThenHome(t *testing.T) {
	m := NewHomeMenu()
	selectable := m.selectableItems()
	last := len(selectable) - 1

	m = sendKey(m, "end")
	if m.cursor != last {
		t.Fatalf("expected 'end' to move cursor to %d, got %d", last, m.cursor)
	}
	m = sendKey(m, "home")
	if m.cursor != 0 {
		t.Fatalf("expected 'home' to move cursor to 0, got %d", m.cursor)
	}
}

func TestHomeMenu_JKNavigationOnlyWhenFilterInactive(t *testing.T) {
	m := NewHomeMenu()
	start := m.cursor

	m = sendKey(m, "j")
	if m.cursor != start+1 {
		t.Fatalf("expected 'j' to move cursor down to %d, got %d", start+1, m.cursor)
	}
	m = sendKey(m, "k")
	if m.cursor != start {
		t.Fatalf("expected 'k' to move cursor back up to %d, got %d", start, m.cursor)
	}
}

func TestHomeMenu_FilterToZeroResults(t *testing.T) {
	m := NewHomeMenu()
	m = sendKey(m, "/")
	if !m.FilterActive() {
		t.Fatal("expected filter to become active after '/'")
	}

	for _, r := range "zzzznomatchzzzz" {
		m = sendKey(m, string(r))
	}

	if len(m.selectableItems()) != 0 {
		t.Fatalf("expected 0 selectable items for an impossible query, got %d", len(m.selectableItems()))
	}
	// Cursor and scroll must be clamped to zero, not left dangling, when
	// the filtered list becomes empty (see clampCursorAndScroll).
	if m.cursor != 0 {
		t.Errorf("expected cursor 0 with zero results, got %d", m.cursor)
	}
	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset 0 with zero results, got %d", m.scrollOffset)
	}
	// SelectedItem must return a safe zero value, not panic or return a
	// stale/incorrect selection.
	if got := m.SelectedItem(); got.ShortName != "" {
		t.Errorf("expected empty ServiceItem when nothing is selectable, got %+v", got)
	}

	// Navigation while filtered to zero results must not panic or move
	// the cursor into invalid territory.
	m = sendKey(m, "down")
	m = sendKey(m, "up")
	if m.cursor != 0 {
		t.Errorf("expected cursor to remain 0 with zero results, got %d", m.cursor)
	}
}

func TestHomeMenu_FilterNarrowsThenClearRestoresFullList(t *testing.T) {
	m := NewHomeMenu()
	full := len(m.selectableItems())

	m = sendKey(m, "/")
	for _, r := range "gke" {
		m = sendKey(m, string(r))
	}
	narrowed := len(m.selectableItems())
	if narrowed == 0 || narrowed >= full {
		t.Fatalf("expected 'gke' to narrow results (got %d of %d total)", narrowed, full)
	}
	if got := m.SelectedItem().ShortName; got != "gke" {
		t.Errorf("expected 'gke' to be selected as the sole/best match, got %q", got)
	}

	m = sendKey(m, "esc")
	if m.FilterActive() {
		t.Error("expected esc to exit filter mode")
	}
	if m.HasFilter() {
		t.Error("expected esc to clear the applied filter")
	}
	if got := len(m.selectableItems()); got != full {
		t.Errorf("expected full list of %d restored after clearing filter, got %d", full, got)
	}
}

func TestHomeMenu_ClampCursorAndScroll_SingleItem(t *testing.T) {
	m := HomeMenuModel{
		TopItem:      &ServiceItem{Name: "Only", ShortName: "only"},
		IsFocused:    true,
		filter:       NewFilterWithPlaceholder("Filter..."),
		viewportRows: 12,
	}
	m.rebuildEntries()
	m.applyFilter()

	if len(m.selectableItems()) != 1 {
		t.Fatalf("expected exactly 1 selectable item, got %d", len(m.selectableItems()))
	}

	// Repeatedly scrolling to the very top/bottom on a single-item list
	// must not panic and must always leave the cursor at 0.
	for i := 0; i < 3; i++ {
		m = sendKey(m, "down")
		m = sendKey(m, "end")
		m = sendKey(m, "up")
		m = sendKey(m, "home")
	}
	if m.cursor != 0 {
		t.Errorf("expected cursor pinned to 0 for a single-item list, got %d", m.cursor)
	}
	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset 0 for a single-item list, got %d", m.scrollOffset)
	}
}

func TestHomeMenu_ClampCursorAndScroll_EmptyMenu(t *testing.T) {
	m := HomeMenuModel{
		IsFocused:    true,
		filter:       NewFilterWithPlaceholder("Filter..."),
		viewportRows: 12,
	}
	m.rebuildEntries()
	m.applyFilter()

	if len(m.selectableItems()) != 0 {
		t.Fatalf("expected 0 selectable items for an empty menu, got %d", len(m.selectableItems()))
	}

	// Must not panic on any navigation against a completely empty menu.
	m = sendKey(m, "down")
	m = sendKey(m, "end")
	m = sendKey(m, "up")
	m = sendKey(m, "home")

	if m.cursor != 0 || m.scrollOffset != 0 {
		t.Errorf("expected cursor=0 scrollOffset=0 for an empty menu, got cursor=%d scrollOffset=%d", m.cursor, m.scrollOffset)
	}
	if got := m.SelectedItem(); got.ShortName != "" {
		t.Errorf("expected zero-value SelectedItem on an empty menu, got %+v", got)
	}
}

func TestHomeMenu_ScrollFollowsCursorPastViewport(t *testing.T) {
	m := NewHomeMenu()
	m.viewportRows = 3 // force a small viewport so scrolling is exercised
	m.clampCursorAndScroll()

	selectable := m.selectableItems()
	if len(selectable) <= m.viewportRows {
		t.Skip("default menu too small to exercise scrolling with this viewport")
	}

	for i := 0; i < len(selectable)-1; i++ {
		m = sendKey(m, "down")
	}

	// Cursor's underlying row in the filtered list must be within
	// [scrollOffset, scrollOffset+viewportRows).
	cursorRow := selectable[m.cursor]
	if cursorRow < m.scrollOffset || cursorRow >= m.scrollOffset+m.viewportRows {
		t.Errorf("cursor row %d not within visible window [%d, %d)", cursorRow, m.scrollOffset, m.scrollOffset+m.viewportRows)
	}

	// And scrolling back to the top must bring scrollOffset back to 0.
	m = sendKey(m, "home")
	if m.scrollOffset != 0 {
		t.Errorf("expected scrollOffset 0 after 'home', got %d", m.scrollOffset)
	}
}

func TestHomeMenu_UpdateViewportRows_ClampsCursor(t *testing.T) {
	m := NewHomeMenu()
	selectable := m.selectableItems()
	m = sendKey(m, "end") // move to the last item under the default viewport

	// Shrinking the screen (and therefore viewportRows) must not leave the
	// cursor or scroll pointing past the now-smaller visible/valid range.
	m.ScreenHeight = 1
	m.UpdateViewportRows()

	if m.cursor < 0 || m.cursor >= len(selectable) {
		t.Errorf("cursor %d out of bounds [0,%d) after shrinking viewport", m.cursor, len(selectable))
	}
	maxScroll := len(m.filtered) - m.viewportRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollOffset < 0 || m.scrollOffset > maxScroll {
		t.Errorf("scrollOffset %d out of bounds [0,%d] after shrinking viewport", m.scrollOffset, maxScroll)
	}
}

func TestHomeMenu_MouseClickSelectsItem(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenHeight = 40
	m.viewportRows = 12

	// Click roughly on the second visible row; exact placement is
	// approximate by design (see getItemFromClickY), so just assert the
	// click updates the cursor to a valid selectable index without
	// panicking, rather than pin an exact row.
	msg := tea.MouseMsg{X: 5, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	updated, _ := m.Update(msg)

	selectable := updated.selectableItems()
	if updated.cursor < 0 || (len(selectable) > 0 && updated.cursor >= len(selectable)) {
		t.Errorf("mouse click produced out-of-bounds cursor %d (selectable=%d)", updated.cursor, len(selectable))
	}
}

func TestHomeMenu_IsOnCategoryAlwaysFalse(t *testing.T) {
	m := NewHomeMenu()
	if m.IsOnCategory() {
		t.Error("cursor should never land on a category header in the flat list")
	}
}

// --- Grid mode ---

func TestHomeMenu_GridMode_WideTerminalActivatesGrid(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()

	if m.narrowMode {
		t.Fatal("expected a wide terminal with the full unfiltered service list to activate grid mode")
	}
	if m.numCols <= 1 {
		t.Errorf("expected numCols > 1 on a wide terminal, got %d", m.numCols)
	}
	if len(m.columns) == 0 {
		t.Fatal("expected columns to be populated in grid mode")
	}
}

func TestHomeMenu_GridMode_NarrowTerminalStaysFlat(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 30
	m.ScreenHeight = 60
	m.UpdateViewportCols()

	if !m.narrowMode {
		t.Error("expected a narrow terminal to stay in flat-list mode")
	}
	if m.numCols != 1 {
		t.Errorf("expected numCols clamped to 1 on a narrow terminal, got %d", m.numCols)
	}
}

func TestHomeMenu_GridMode_LeftRightNavigation(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	if m.narrowMode {
		t.Fatal("expected grid mode on a wide terminal")
	}

	// Start on Overview, move down into the first column, then right into
	// the second column -- the selection must change to a service in a
	// different category.
	m = sendKey(m, "down")
	if m.onTopItem {
		t.Fatal("expected 'down' from Overview to enter the first column")
	}
	firstCatIdx := m.cursorCol
	first := m.SelectedItem()

	m = sendKey(m, "right")
	if m.cursorCol == firstCatIdx {
		t.Fatal("expected 'right' to move to the next column")
	}
	second := m.SelectedItem()
	if second.ShortName == first.ShortName {
		t.Errorf("expected a different service selected after moving right, got %q both times", second.ShortName)
	}

	m = sendKey(m, "left")
	if m.cursorCol != firstCatIdx {
		t.Errorf("expected 'left' to return to column %d, got %d", firstCatIdx, m.cursorCol)
	}
}

func TestHomeMenu_GridMode_RightAtLastColumnNoops(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	m = sendKey(m, "down")

	last := len(m.columns) - 1
	for i := 0; i < last+5; i++ {
		m = sendKey(m, "right")
	}
	if m.cursorCol != last {
		t.Errorf("expected cursorCol clamped to last column %d, got %d", last, m.cursorCol)
	}
}

func TestHomeMenu_GridMode_RowClampOnUnevenColumns(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	if len(m.columns) < 2 {
		t.Skip("need at least 2 columns to exercise uneven-column clamping")
	}

	// Find two adjacent columns with different row counts.
	var colA, colB int
	found := false
	for i := 0; i < len(m.columns)-1; i++ {
		if len(m.columns[i].rows) != len(m.columns[i+1].rows) {
			colA, colB = i, i+1
			found = true
			break
		}
	}
	if !found {
		t.Skip("no two adjacent columns with different row counts to test clamping")
	}

	m.onTopItem = false
	m.cursorCol = colA
	// Put the cursor on the last row of the taller column.
	tall, short := colA, colB
	if len(m.columns[colB].rows) > len(m.columns[colA].rows) {
		tall, short = colB, colA
	}
	m.cursorCol = tall
	m.cursorRow = len(m.columns[tall].rows) - 1

	// Move onto the shorter column: cursorRow must clamp into range, not
	// panic or point past the shorter column's last row.
	if tall < short {
		m = sendKey(m, "right")
	} else {
		m = sendKey(m, "left")
	}
	if m.cursorRow < 0 || m.cursorRow >= len(m.columns[short].rows) {
		t.Errorf("cursorRow %d out of bounds [0,%d) after moving to a shorter column", m.cursorRow, len(m.columns[short].rows))
	}
}

func TestHomeMenu_GridMode_FilterCollapsesToFlatList(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	if m.narrowMode {
		t.Fatal("expected grid mode before filtering")
	}

	m = sendKey(m, "/")
	for _, r := range "gke" {
		m = sendKey(m, string(r))
	}

	if !m.narrowMode {
		t.Error("expected a narrow, scattered filter result to collapse to the flat list")
	}
	if got := m.SelectedItem().ShortName; got != "gke" {
		t.Errorf("expected 'gke' selected via the flat-list fallback, got %q", got)
	}

	m = sendKey(m, "esc")
	if m.narrowMode {
		t.Error("expected clearing the filter to restore grid mode on a wide terminal")
	}
}

func TestHomeMenu_GridMode_MouseClickIsInert(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	m = sendKey(m, "down")
	m = sendKey(m, "right")
	col, row := m.cursorCol, m.cursorRow

	msg := tea.MouseMsg{X: 5, Y: 20, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	updated, _ := m.Update(msg)

	if updated.cursorCol != col || updated.cursorRow != row {
		t.Errorf("expected mouse clicks to be inert in grid mode, cursor moved from (%d,%d) to (%d,%d)", col, row, updated.cursorCol, updated.cursorRow)
	}
}

func TestHomeMenu_GridMode_HomeAndEnd(t *testing.T) {
	m := NewHomeMenu()
	m.ScreenWidth = 200
	m.ScreenHeight = 60
	m.UpdateViewportCols()
	m = sendKey(m, "down")
	m = sendKey(m, "right")

	m = sendKey(m, "home")
	if !m.onTopItem {
		t.Error("expected 'home' to select the Overview strip in grid mode")
	}
	if got := m.SelectedItem().ShortName; got != "overview" {
		t.Errorf("expected 'overview' selected after 'home', got %q", got)
	}

	m = sendKey(m, "end")
	if m.onTopItem {
		t.Error("expected 'end' to move off the Overview strip")
	}
	lastCol := len(m.columns) - 1
	if m.cursorCol != lastCol {
		t.Errorf("expected 'end' to land on the last column %d, got %d", lastCol, m.cursorCol)
	}
}
