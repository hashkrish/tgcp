package components

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"github.com/yogirk/tgcp/internal/styles"
)

// Category represents a group of services
type Category struct {
	Name     string
	Expanded bool
	Services []ServiceItem
}

// serviceIcons maps service short names to icons (mirrors sidebar).
//
// These are deliberately plain ASCII, not Unicode symbols/dingbats. The
// Geometric Shapes / Dingbats glyphs used here previously (⚙ ☸ ▷ ▤ ◔ ⛁ ⬡ ▦
// ◇ ◲ ⊞ ⇢ ⎈ ⇌ ⚿ ✦ ⇄ ☰ ◈ ▣ ◉) have ambiguous East-Asian width: Bubble
// Tea/lipgloss's width library measures them all as 1 column, but some
// terminals — confirmed here specifically inside tmux — render at least one
// of them as 2 columns. That single-column disagreement compounds across
// every row below it, permanently misaligning the list (was misdiagnosed
// at first as a rendering race, since it only became visible/obvious after
// scrolling). ASCII is unambiguously 1 column everywhere, so this class of
// bug can't recur.
var serviceIcons = map[string]string{
	"overview":         "@",
	"gce":              "#",
	"gke":              "K",
	"run":              ">",
	"functions":        "f",
	"gcs":              "S",
	"disks":            "D",
	"filestore":        "V",
	"sql":              "Q",
	"spanner":          "N",
	"bigtable":         "T",
	"redis":            "M",
	"firestore":        "F",
	"bq":               "B",
	"dataflow":         "~",
	"dataproc":         "%",
	"pubsub":           "P",
	"scheduler":        "Z",
	"cloudtasks":       "X",
	"iam":              "&",
	"secrets":          "$",
	"parametermanager": "!",
	"net":              "=",
	"loadbalancing":    "+",
	"dns":              ":",
	"kms":              "*",
	"logs":             "L",
	"monitoring":       "O",
	"cloudbuild":       "^",
	"artifactregistry": "A",
}

// gridColumn is one category's worth of services, rendered as a column in
// the grid layout (see rebuildColumns/renderGrid). Grid mode is used instead
// of the flat list when the terminal is wide enough and there are enough
// results for multiple columns to be worthwhile; otherwise HomeMenuModel
// falls back to the flat list unchanged (see narrowMode).
type gridColumn struct {
	categoryIdx  int
	categoryName string
	rows         []listEntry // service entries only (isCategory==false, service != nil)
}

// listEntry represents a single row in the flat list (either category header or service)
type listEntry struct {
	isCategory   bool
	isTopItem    bool
	categoryIdx  int
	serviceIdx   int
	service      *ServiceItem // nil for category headers
	categoryName string
	searchText   string // "Name ShortName" for fuzzy matching
}

type HomeMenuModel struct {
	TopItem    *ServiceItem
	Categories []Category
	IsFocused  bool

	// Screen dimensions
	ScreenWidth  int
	ScreenHeight int

	filter       FilterModel
	allEntries   []listEntry // flat list: top item + categories + services
	filtered     []listEntry // after fuzzy filter
	cursor       int         // index into selectable items in filtered list
	scrollOffset int
	viewportRows int
	matchIndexes map[int][]int // allEntries index -> matched char positions

	// contentWidth is a fixed box content width computed once from ALL
	// entries (not just the currently-visible slice). Sizing the box to
	// only the visible lines would make its width change every time the
	// scroll position changes (short names vs. long names), shifting the
	// whole centered landing-page layout left/right between frames.
	contentWidth int

	// --- Grid mode (category-column layout) ---
	// The flat list above (allEntries/filtered/cursor/scrollOffset/
	// viewportRows) is left completely unchanged and remains the fallback
	// path -- see narrowMode.
	numCols     int          // columns per row-of-columns; only meaningful when !narrowMode
	widthNarrow bool         // true if the terminal is too narrow for >1 column (set by UpdateViewportCols)
	narrowMode  bool         // true => render/navigate via the flat list; false => grid
	columns     []gridColumn // rebuilt by rebuildColumns() alongside every applyFilter()/UpdateViewportCols() call
	cursorCol   int          // grid-mode cursor: column index into columns
	cursorRow   int          // grid-mode cursor: row index into columns[cursorCol].rows
	onTopItem   bool         // grid-mode cursor: true when the Overview strip (not a column) is selected
}

func NewHomeMenu() HomeMenuModel {
	m := HomeMenuModel{
		TopItem: &ServiceItem{Name: "Overview (Command Center)", ShortName: "overview", Active: true},
		Categories: []Category{
			{
				Name:     "Compute",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "Compute Engine (GCE)", ShortName: "gce"},
					{Name: "Kubernetes Engine (GKE)", ShortName: "gke"},
					{Name: "Cloud Run", ShortName: "run"},
					{Name: "Cloud Functions", ShortName: "functions"},
				},
			},
			{
				Name:     "Storage",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "Cloud Storage (GCS)", ShortName: "gcs"},
					{Name: "Disks (Block Storage)", ShortName: "disks"},
					{Name: "Filestore (NFS)", ShortName: "filestore"},
				},
			},
			{
				Name:     "Databases",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "Cloud SQL", ShortName: "sql"},
					{Name: "Spanner", ShortName: "spanner"},
					{Name: "Bigtable", ShortName: "bigtable"},
					{Name: "Memorystore (Redis)", ShortName: "redis"},
					{Name: "Firestore / Datastore", ShortName: "firestore"},
				},
			},
			{
				Name:     "Data & Analytics",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "BigQuery", ShortName: "bq"},
					{Name: "Dataflow", ShortName: "dataflow"},
					{Name: "Dataproc", ShortName: "dataproc"},
					{Name: "Pub/Sub", ShortName: "pubsub"},
					{Name: "Cloud Scheduler", ShortName: "scheduler"},
					{Name: "Cloud Tasks", ShortName: "cloudtasks"},
				},
			},
			{
				Name:     "Security & Networking",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "IAM & Admin", ShortName: "iam"},
					{Name: "Secret Manager", ShortName: "secrets"},
					{Name: "Parameter Manager", ShortName: "parametermanager"},
					{Name: "VPC Network", ShortName: "net"},
					{Name: "Load Balancing", ShortName: "loadbalancing"},
					{Name: "Cloud DNS", ShortName: "dns"},
					{Name: "Cloud KMS", ShortName: "kms"},
				},
			},
			{
				Name:     "Observability",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "Cloud Logging", ShortName: "logs"},
					{Name: "Cloud Monitoring", ShortName: "monitoring"},
				},
			},
			{
				Name:     "DevOps",
				Expanded: true,
				Services: []ServiceItem{
					{Name: "Cloud Build", ShortName: "cloudbuild"},
					{Name: "Artifact Registry", ShortName: "artifactregistry"},
				},
			},
		},
		IsFocused:    true,
		filter:       NewFilterWithPlaceholder("Type to filter services..."),
		viewportRows: 12, // conservative default before first WindowSizeMsg
		// Grid mode only activates once UpdateViewportCols() sees a real,
		// wide-enough ScreenWidth from a WindowSizeMsg; until then (and on
		// any narrow terminal) everything behaves exactly like the
		// original flat list, so every existing flat-mode test/behavior is
		// unaffected by grid mode's addition.
		numCols:     1,
		widthNarrow: true,
		narrowMode:  true,
		onTopItem:   true,
	}
	m.rebuildEntries()
	m.applyFilter()
	return m
}

// rebuildEntries builds the flat list from TopItem + Categories
func (m *HomeMenuModel) rebuildEntries() {
	m.allEntries = nil

	if m.TopItem != nil {
		m.allEntries = append(m.allEntries, listEntry{
			isTopItem:  true,
			service:    m.TopItem,
			searchText: m.TopItem.Name + " " + m.TopItem.ShortName,
		})
	}

	for catIdx, cat := range m.Categories {
		m.allEntries = append(m.allEntries, listEntry{
			isCategory:   true,
			categoryIdx:  catIdx,
			categoryName: cat.Name,
		})
		for svcIdx := range cat.Services {
			svc := &m.Categories[catIdx].Services[svcIdx]
			m.allEntries = append(m.allEntries, listEntry{
				categoryIdx: catIdx,
				serviceIdx:  svcIdx,
				service:     svc,
				searchText:  svc.Name + " " + svc.ShortName,
			})
		}
	}

	m.contentWidth = m.computeContentWidth()
}

// computeContentWidth measures the widest possible rendered line across ALL
// entries (every category header and service name, not just whichever are
// currently scrolled into view) so the box width stays constant regardless
// of scroll position. It renders through the exact same styles View() uses
// (including their padding/border, which differs between selected and
// unselected rows) rather than estimating from raw text length, so the
// measurement can't drift out of sync with the actual render.
func (m *HomeMenuModel) computeContentWidth() int {
	widest := lipgloss.Width(styles.HeaderStyle.Render("Services"))
	if w := lipgloss.Width("  ↑ more"); w > widest {
		widest = w
	}
	catStyle := lipgloss.NewStyle().Bold(true).PaddingLeft(styles.SpaceS)
	for _, e := range m.allEntries {
		var w int
		if e.isCategory {
			w = lipgloss.Width(catStyle.Render(strings.ToUpper(e.categoryName)))
		} else if e.service != nil {
			iconGlyph := serviceIcons[e.service.ShortName]
			if iconGlyph == "" {
				iconGlyph = "·"
			}
			name := e.service.Name
			if e.service.IsComing {
				name += " [Coming Soon]"
			}
			display := iconGlyph + "  " + name
			if sw := lipgloss.Width(styles.SelectedActive.Render(display)); sw > w {
				w = sw
			}
			if sw := lipgloss.Width(styles.UnselectedItemStyle.Render(display)); sw > w {
				w = sw
			}
		}
		if w > widest {
			widest = w
		}
	}
	return widest
}

// applyFilter filters the flat list based on the current filter query
func (m *HomeMenuModel) applyFilter() {
	query := m.filter.Value()
	m.matchIndexes = make(map[int][]int)

	if query == "" {
		// No filter — show everything
		m.filtered = make([]listEntry, len(m.allEntries))
		copy(m.filtered, m.allEntries)
		m.updateFilterCounts()
		m.clampCursorAndScroll()
		m.rebuildColumns()
		return
	}

	// Collect searchable entries (services only) with their allEntries indices
	type searchable struct {
		idx  int
		text string
	}
	var targets []searchable
	var texts []string
	for i, e := range m.allEntries {
		if e.service != nil {
			targets = append(targets, searchable{idx: i, text: e.searchText})
			texts = append(texts, e.searchText)
		}
	}

	// Run fuzzy match
	matches := fuzzy.Find(query, texts)
	matchedEntryIdxs := make(map[int]bool)
	for _, match := range matches {
		entryIdx := targets[match.Index].idx
		matchedEntryIdxs[entryIdx] = true
		m.matchIndexes[entryIdx] = match.MatchedIndexes
	}

	// Build filtered list: include category headers only if they have matching services
	m.filtered = nil
	for i, e := range m.allEntries {
		if e.isCategory {
			if m.categoryHasMatch(e.categoryIdx, matchedEntryIdxs) {
				m.filtered = append(m.filtered, e)
			}
		} else if matchedEntryIdxs[i] {
			m.filtered = append(m.filtered, e)
		}
	}

	// Reset cursor to first match when filtering
	m.cursor = 0
	m.scrollOffset = 0
	m.updateFilterCounts()
	m.clampCursorAndScroll()
	m.rebuildColumns()
}

// rebuildColumns groups the current m.filtered service entries by category
// into columns (see gridColumn), and decides narrowMode: whether the grid is
// actually usable given the current width and whether a search filter is
// active. Called after every applyFilter() (so filtering collapses to the
// flat list -- see the narrowMode comment below for why) and after every
// UpdateViewportCols() (so resizing re-flows the grid).
func (m *HomeMenuModel) rebuildColumns() {
	byCat := make(map[int][]listEntry)
	var catOrder []int
	catName := make(map[int]string)

	for _, e := range m.filtered {
		if e.isCategory {
			catName[e.categoryIdx] = e.categoryName
			continue
		}
		if e.isTopItem || e.service == nil {
			continue // TopItem is handled separately (see onTopItem), not as a column row
		}
		if _, ok := byCat[e.categoryIdx]; !ok {
			catOrder = append(catOrder, e.categoryIdx)
		}
		byCat[e.categoryIdx] = append(byCat[e.categoryIdx], e)
	}

	columns := make([]gridColumn, 0, len(catOrder))
	for _, ci := range catOrder {
		columns = append(columns, gridColumn{categoryIdx: ci, categoryName: catName[ci], rows: byCat[ci]})
	}
	m.columns = columns

	// Collapse to the flat list whenever the terminal is narrow
	// (widthNarrow), OR whenever a search query is active at all. Grid mode
	// is a 2D layout -- "down" only moves within the current column/
	// category -- which breaks the universal "type a query, press down to
	// reach the next match" fuzzy-finder expectation the moment matches
	// land in more than one category/column (a match further down the
	// result list can become unreachable via down alone, since reaching it
	// requires pressing "right" into a different column first, which
	// nothing hints at). The flat list's linear navigation is exactly what
	// search expects, so any non-empty filter always uses it, regardless
	// of how many/few matches there are.
	m.narrowMode = m.widthNarrow || m.filter.Value() != ""

	// Clamp the 2D cursor into range. Mode switches (narrow<->grid) don't
	// try to preserve which service was selected across the transition --
	// an acceptable v1 rough edge, not attempted here.
	if len(m.columns) == 0 {
		m.cursorCol, m.cursorRow = 0, 0
		return
	}
	if m.cursorCol >= len(m.columns) {
		m.cursorCol = len(m.columns) - 1
	}
	if m.cursorCol < 0 {
		m.cursorCol = 0
	}
	m.clampCursorRowForColumn()
}

// clampCursorRowForColumn clamps cursorRow into the current cursorCol's row
// count. Used both by rebuildColumns() and by left/right column movement,
// where the target column may have fewer rows than the one the cursor came
// from -- clamping to the target's last row is intentional (not a bug):
// no attempt is made to preserve visual Y-position across columns of
// different heights.
func (m *HomeMenuModel) clampCursorRowForColumn() {
	if m.cursorCol < 0 || m.cursorCol >= len(m.columns) {
		m.cursorRow = 0
		return
	}
	rows := m.columns[m.cursorCol].rows
	if m.cursorRow >= len(rows) {
		m.cursorRow = len(rows) - 1
	}
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}
}

// --- Grid-mode navigation ---
// Mirrors the flat list's up/down/home/end semantics (no wrap-around at
// either end) plus a new left/right axis for moving between columns.

// gridMoveDown moves within the current column, or from the Overview strip
// into the current column's first row.
func (m *HomeMenuModel) gridMoveDown() {
	if m.onTopItem {
		if len(m.columns) > 0 {
			m.onTopItem = false
			m.cursorRow = 0
		}
		return
	}
	if m.cursorCol < 0 || m.cursorCol >= len(m.columns) {
		return
	}
	if m.cursorRow < len(m.columns[m.cursorCol].rows)-1 {
		m.cursorRow++
	}
}

// gridMoveUp moves within the current column; from a column's top row, it
// jumps to the Overview strip only if that column is in the first
// row-of-columns (columns wrap into multiple rows-of-columns when there
// are more categories than numCols) -- avoids conflating the row axis with
// the column axis for columns further down the grid.
func (m *HomeMenuModel) gridMoveUp() {
	if m.onTopItem {
		return
	}
	if m.cursorRow > 0 {
		m.cursorRow--
		return
	}
	if m.numCols > 0 && m.cursorCol < m.numCols {
		m.onTopItem = true
	}
}

// gridMoveLeft moves to the previous column in reading order (columns are
// stored left-to-right, top-to-bottom across rows-of-columns, so this
// naturally falls through to the last column of the previous
// row-of-columns at a row's leftmost column -- no separate wrap logic
// needed). No-ops at the very first column, and while on the Overview strip.
func (m *HomeMenuModel) gridMoveLeft() {
	if m.onTopItem || len(m.columns) == 0 {
		return
	}
	if m.cursorCol > 0 {
		m.cursorCol--
		m.clampCursorRowForColumn()
	}
}

// gridMoveRight is the mirror of gridMoveLeft.
func (m *HomeMenuModel) gridMoveRight() {
	if m.onTopItem || len(m.columns) == 0 {
		return
	}
	if m.cursorCol < len(m.columns)-1 {
		m.cursorCol++
		m.clampCursorRowForColumn()
	}
}

// gridHome jumps to the Overview strip (top-left), matching the flat list's
// "home" landing on cursor 0 (the TopItem).
func (m *HomeMenuModel) gridHome() {
	m.onTopItem = true
}

// gridEnd jumps to the last row of the last column (bottom-right), matching
// the flat list's "end" landing on the very last item.
func (m *HomeMenuModel) gridEnd() {
	if len(m.columns) == 0 {
		return
	}
	m.onTopItem = false
	m.cursorCol = len(m.columns) - 1
	m.cursorRow = len(m.columns[m.cursorCol].rows) - 1
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}
}

// categoryHasMatch checks if any service in the given category matched the filter
func (m *HomeMenuModel) categoryHasMatch(catIdx int, matchedIdxs map[int]bool) bool {
	for i, e := range m.allEntries {
		if !e.isCategory && e.categoryIdx == catIdx && matchedIdxs[i] {
			return true
		}
	}
	return false
}

// updateFilterCounts sets the filter bar match/total counts
func (m *HomeMenuModel) updateFilterCounts() {
	// Count total selectable items (services)
	total := 0
	for _, e := range m.allEntries {
		if e.service != nil {
			total++
		}
	}
	matched := 0
	for _, e := range m.filtered {
		if e.service != nil {
			matched++
		}
	}
	m.filter.SetMatchCounts(total, matched)
}

// selectableItems returns only selectable entries from the filtered list
func (m *HomeMenuModel) selectableItems() []int {
	var indices []int
	for i, e := range m.filtered {
		if e.service != nil {
			indices = append(indices, i)
		}
	}
	return indices
}

// clampCursorAndScroll ensures cursor and scroll are within valid bounds
func (m *HomeMenuModel) clampCursorAndScroll() {
	selectable := m.selectableItems()
	if len(selectable) == 0 {
		m.cursor = 0
		m.scrollOffset = 0
		return
	}
	if m.cursor >= len(selectable) {
		m.cursor = len(selectable) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.adjustScroll()
}

// adjustScroll ensures the cursor row is visible in the viewport
func (m *HomeMenuModel) adjustScroll() {
	selectable := m.selectableItems()
	if len(selectable) == 0 || m.viewportRows <= 0 {
		return
	}

	// The cursor's actual row in the filtered list
	cursorRow := 0
	if m.cursor < len(selectable) {
		cursorRow = selectable[m.cursor]
	}

	// Ensure cursor row is visible
	if cursorRow < m.scrollOffset {
		m.scrollOffset = cursorRow
	}
	if cursorRow >= m.scrollOffset+m.viewportRows {
		m.scrollOffset = cursorRow - m.viewportRows + 1
	}

	// Clamp scroll offset
	maxScroll := len(m.filtered) - m.viewportRows
	if maxScroll < 0 {
		maxScroll = 0
	}
	if m.scrollOffset > maxScroll {
		m.scrollOffset = maxScroll
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
}

// isArrowNavKey returns true for non-printable navigation keys that should
// pass through to cursor movement even when the filter input has focus.
func isArrowNavKey(key string) bool {
	return key == "up" || key == "down" ||
		key == "home" || key == "end" ||
		key == "pageup" || key == "pagedown" ||
		key == "ctrl+p" || key == "ctrl+n"
}

func (m HomeMenuModel) Init() tea.Cmd {
	return nil
}

func (m HomeMenuModel) Update(msg tea.Msg) (HomeMenuModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		key := msg.String()

		// Clear applied filter with q or esc (when filter is not active but has a value)
		if !m.filter.Active && m.filter.Value() != "" && (key == "q" || key == "esc") {
			m.filter.TextInput.Reset()
			m.applyFilter()
			return m, nil
		}

		// Let filter handle its lifecycle (enter/exit/typing)
		if m.filter.Active || key == "/" {
			shouldExit, _, cmd := m.filter.HandleKeyMsg(msg)

			if shouldExit {
				m.applyFilter()
				return m, cmd
			}

			if m.filter.Active {
				// Only non-printable nav keys fall through; printable keys stay in filter
				if isArrowNavKey(key) {
					m.applyFilter()
					// fall through to navigation below
				} else {
					m.applyFilter()
					return m, cmd
				}
			} else if cmd != nil {
				// Just entered filter mode
				return m, cmd
			}
		}

		// Navigation (works whether filter is active or not)
		if m.narrowMode {
			selectable := m.selectableItems()
			switch key {
			case "up", "ctrl+p":
				if m.cursor > 0 {
					m.cursor--
					m.adjustScroll()
				}
			case "k":
				if !m.filter.Active && m.cursor > 0 {
					m.cursor--
					m.adjustScroll()
				}
			case "down", "ctrl+n":
				if m.cursor < len(selectable)-1 {
					m.cursor++
					m.adjustScroll()
				}
			case "j":
				if !m.filter.Active && m.cursor < len(selectable)-1 {
					m.cursor++
					m.adjustScroll()
				}
			case "home", "g":
				if !m.filter.Active {
					m.cursor = 0
					m.adjustScroll()
				}
			case "end", "G":
				if !m.filter.Active && len(selectable) > 0 {
					m.cursor = len(selectable) - 1
					m.adjustScroll()
				}
			}
		} else {
			// Grid mode: 2D column/row cursor (see gridMove*/gridHome/gridEnd).
			switch key {
			case "up", "ctrl+p":
				m.gridMoveUp()
			case "k":
				if !m.filter.Active {
					m.gridMoveUp()
				}
			case "down", "ctrl+n":
				m.gridMoveDown()
			case "j":
				if !m.filter.Active {
					m.gridMoveDown()
				}
			case "left":
				m.gridMoveLeft()
			case "h":
				if !m.filter.Active {
					m.gridMoveLeft()
				}
			case "right":
				m.gridMoveRight()
			case "l":
				if !m.filter.Active {
					m.gridMoveRight()
				}
			case "home", "g":
				if !m.filter.Active {
					m.gridHome()
				}
			case "end", "G":
				if !m.filter.Active {
					m.gridEnd()
				}
			}
		}

	case tea.MouseMsg:
		// Mouse click-to-select only works in the flat-list fallback for
		// now -- the grid's per-cell bounding boxes aren't tracked during
		// render (unlike the flat list's approximate single-column
		// getItemFromClickY), so grid mode is keyboard-only in this pass.
		if m.narrowMode && msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			// Map click Y to a filtered list entry
			if idx := m.getItemFromClickY(msg.Y); idx >= 0 {
				// Find which selectable index this corresponds to
				selectable := m.selectableItems()
				for si, fi := range selectable {
					if fi == idx {
						m.cursor = si
						break
					}
				}
			}
		}
	}
	return m, nil
}

// getItemFromClickY maps a screen Y to a filtered list index (best effort)
func (m HomeMenuModel) getItemFromClickY(screenY int) int {
	// The menu is rendered inside a centered layout. We estimate:
	// filter bar takes 1 row, then blank row, then list items
	// This is approximate since we don't know the exact placement.
	// We'll treat relative Y = screenY offset from estimated content start.
	// For now, use scroll offset to map.
	const filterBarRows = 2 // filter bar + blank line
	const boxPadding = 1    // top padding of box
	const titleRows = 2     // "Services" + blank line

	// Rough estimate of where the menu box content starts
	// The landing page centers content vertically, so estimate ~40% down
	menuStartY := m.ScreenHeight * 2 / 5
	relY := screenY - menuStartY - boxPadding - titleRows - filterBarRows

	if relY < 0 {
		return -1
	}

	targetRow := m.scrollOffset + relY
	if targetRow >= 0 && targetRow < len(m.filtered) {
		return targetRow
	}
	return -1
}

func (m HomeMenuModel) View() string {
	if m.narrowMode {
		return m.renderFlatList()
	}
	return m.renderGrid()
}

// renderFlatList is the original single-column, vertically-scrolling menu.
// It's the fallback for both narrow terminals and small/scattered filter
// results (see rebuildColumns) -- kept entirely unchanged from before grid
// mode existed.
func (m HomeMenuModel) renderFlatList() string {
	// Filter bar
	filterBar := m.filter.View()

	// Title
	title := styles.HeaderStyle.Render("Services")

	// Category header style — accent colour per category so the list has
	// a visible rhythm as you scroll.
	catStyleFor := func(name string) lipgloss.Style {
		return lipgloss.NewStyle().
			Foreground(CategoryColor(name)).
			Bold(true).
			PaddingLeft(styles.SpaceS)
	}

	// Visible slice
	endIdx := m.scrollOffset + m.viewportRows
	if endIdx > len(m.filtered) {
		endIdx = len(m.filtered)
	}
	startIdx := m.scrollOffset
	if startIdx < 0 {
		startIdx = 0
	}

	// Build the selectable items map for cursor highlighting
	selectable := m.selectableItems()
	cursorFilteredIdx := -1
	if m.cursor >= 0 && m.cursor < len(selectable) {
		cursorFilteredIdx = selectable[m.cursor]
	}

	var lines []string
	for i := startIdx; i < endIdx; i++ {
		entry := m.filtered[i]
		if entry.isCategory {
			// Category header: uppercase, accent-coloured, non-selectable
			header := catStyleFor(entry.categoryName).Render(strings.ToUpper(entry.categoryName))
			lines = append(lines, header)
		} else if entry.service != nil {
			// Service item — tint the icon by category; keep the label text
			// neutral so the list stays readable.
			iconGlyph := serviceIcons[entry.service.ShortName]
			if iconGlyph == "" {
				iconGlyph = "·"
			}
			icon := lipgloss.NewStyle().
				Foreground(ServiceAccent(entry.service.ShortName)).
				Render(iconGlyph)
			name := entry.service.Name
			if entry.service.IsComing {
				name += " [Coming Soon]"
			}

			display := icon + "  " + name
			isSelected := i == cursorFilteredIdx

			if isSelected {
				rendered := styles.SelectedActive.Render(display)
				lines = append(lines, rendered)
			} else {
				style := styles.UnselectedItemStyle
				if entry.service.IsComing {
					style = style.Foreground(styles.ColorTextMuted)
				}
				lines = append(lines, style.Render(display))
			}
		}
	}

	// Scroll indicators — always reserve both lines (blank when not needed)
	// so the box renders the same total height at every scroll position.
	// Otherwise the frame height flickers between renders as you scroll,
	// and since lipgloss.Height() only pads (never truncates), a taller
	// frame leaves stale content behind when the next frame shrinks.
	scrollUp := ""
	if m.scrollOffset > 0 {
		scrollUp = styles.SubtleStyle.Render("  ↑ more")
	}
	scrollDown := ""
	if m.scrollOffset+m.viewportRows < len(m.filtered) {
		scrollDown = styles.SubtleStyle.Render("  ↓ more")
	}

	// Combine list with scroll indicators
	listContent := lipgloss.JoinVertical(lipgloss.Left, scrollUp, lipgloss.JoinVertical(lipgloss.Left, lines...), scrollDown)

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", filterBar, "", listContent)

	// Fixed-height, fixed-width box so the menu doesn't move or resize as
	// you scroll — contentWidth is computed from ALL entries (see
	// computeContentWidth), not just the ones currently visible.
	// NOTE: .Height() sets the content+padding height BEFORE the border is
	// added — lipgloss appends the top/bottom border rows on top of this,
	// so this value must NOT include the border's own 2 rows (that was a
	// prior bug here: passing viewportRows+10 made the box 2 rows taller
	// than the overhead budget in UpdateViewportRows assumed).
	boxHeight := m.viewportRows + 8 // items + title(1) + gaps(2) + filter(1) + padding(2) + scroll indicators(2); border(2) added separately by lipgloss
	textWidth := m.contentWidth
	if fw := lipgloss.Width(filterBar); fw > textWidth {
		textWidth = fw
	}
	// PrimaryBoxStyle.Width() sets the TOTAL width including its own
	// horizontal padding (lipgloss wraps at width-leftPadding-rightPadding),
	// so add that padding back or the filter bar/service names wrap onto
	// an extra line — which then desyncs the fixed-height budget above.
	boxWidth := textWidth + 2*styles.SpaceM
	menuBox := styles.PrimaryBoxStyle.
		Width(boxWidth).
		Height(boxHeight).
		Render(content)

	return menuBox
}

// measureServiceRowWidth returns the rendered width of one service row
// (icon + name), checked against both selected/unselected styles since
// their padding can differ -- mirrors computeContentWidth's per-row
// measurement technique (rendering through the real styles rather than
// estimating from raw text length, so it can't drift out of sync with the
// actual render), scoped to a single row for per-column width measurement
// in grid mode.
func measureServiceRowWidth(e listEntry) int {
	if e.service == nil {
		return 0
	}
	iconGlyph := serviceIcons[e.service.ShortName]
	if iconGlyph == "" {
		iconGlyph = "·"
	}
	name := e.service.Name
	if e.service.IsComing {
		name += " [Coming Soon]"
	}
	display := iconGlyph + "  " + name
	w := lipgloss.Width(styles.SelectedActive.Render(display))
	if uw := lipgloss.Width(styles.UnselectedItemStyle.Render(display)); uw > w {
		w = uw
	}
	return w
}

// maxGridColWidth caps a single category column's width so one long service
// name (e.g. "Memorystore (Redis)") can't blow out its whole column; names
// wider than this are truncated with an ellipsis (see truncateToWidth).
const maxGridColWidth = 32

// renderColumn renders one category's header + service rows as a single
// fixed-width block, used by renderGrid.
func (m HomeMenuModel) renderColumn(col gridColumn, colIdx int) string {
	catHeaderStyle := lipgloss.NewStyle().
		Foreground(CategoryColor(col.categoryName)).
		Bold(true).
		PaddingLeft(styles.SpaceS)
	header := catHeaderStyle.Render(strings.ToUpper(col.categoryName))

	width := lipgloss.Width(header)
	for _, e := range col.rows {
		if w := measureServiceRowWidth(e); w > width {
			width = w
		}
	}
	if width > maxGridColWidth {
		width = maxGridColWidth
	}

	lines := []string{header}
	for ri, e := range col.rows {
		if e.service == nil {
			continue
		}
		iconGlyph := serviceIcons[e.service.ShortName]
		if iconGlyph == "" {
			iconGlyph = "·"
		}
		name := e.service.Name
		if e.service.IsComing {
			name += " [Coming Soon]"
		}
		// Truncate the name (not the icon) if this row would exceed the
		// column's capped width.
		if plainWidth := lipgloss.Width(iconGlyph + "  " + name); plainWidth > width {
			avail := width - lipgloss.Width(iconGlyph+"  ")
			name = truncateToWidth(name, avail)
		}
		icon := lipgloss.NewStyle().Foreground(ServiceAccent(e.service.ShortName)).Render(iconGlyph)
		display := icon + "  " + name

		isSelected := !m.onTopItem && colIdx == m.cursorCol && ri == m.cursorRow
		if isSelected {
			lines = append(lines, styles.SelectedActive.Render(display))
		} else {
			style := styles.UnselectedItemStyle
			if e.service.IsComing {
				style = style.Foreground(styles.ColorTextMuted)
			}
			lines = append(lines, style.Render(display))
		}
	}

	return lipgloss.NewStyle().Width(width).Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// renderGrid renders the category-column grid layout (see rebuildColumns/
// gridColumn). Columns wrap into additional rows-of-columns when there are
// more categories than numCols. Unlike renderFlatList, there is no vertical
// scrolling of the grid itself in this pass -- with today's ~7 categories
// and a 4-column cap, the grid is at most 2 rows-of-columns tall, which
// comfortably fits typical terminal heights; a growing category count would
// need this revisited (see the risk callout in the design notes).
func (m HomeMenuModel) renderGrid() string {
	filterBar := m.filter.View()
	title := styles.HeaderStyle.Render("Services")

	// Overview strip — a standalone always-first row above the columns,
	// same special treatment it gets in the flat list.
	var topLine string
	if m.TopItem != nil {
		iconGlyph := serviceIcons[m.TopItem.ShortName]
		if iconGlyph == "" {
			iconGlyph = "·"
		}
		display := iconGlyph + "  " + m.TopItem.Name
		if m.onTopItem {
			topLine = styles.SelectedActive.Render(display)
		} else {
			topLine = styles.UnselectedItemStyle.Render(display)
		}
	}

	var gridBody string
	if len(m.columns) == 0 {
		gridBody = EmptyState("services")
	} else {
		const colGap = "    "
		var screenRows []string
		for start := 0; start < len(m.columns); start += m.numCols {
			end := start + m.numCols
			if end > len(m.columns) {
				end = len(m.columns)
			}
			blocks := make([]string, 0, (end-start)*2-1)
			for i := start; i < end; i++ {
				if i > start {
					blocks = append(blocks, colGap)
				}
				blocks = append(blocks, m.renderColumn(m.columns[i], i))
			}
			screenRows = append(screenRows, lipgloss.JoinHorizontal(lipgloss.Top, blocks...))
		}
		gridBody = lipgloss.JoinVertical(lipgloss.Left, screenRows...)
	}

	content := lipgloss.JoinVertical(lipgloss.Left, title, "", filterBar, "", topLine, "", gridBody)

	// boxWidth: widest screen-row-of-columns, so the box doesn't jump width
	// between filter states (same anti-drift intent as the flat list's
	// computeContentWidth, just measured from the actually-rendered grid
	// content here since column packing/wrapping makes an upfront measure
	// impractical).
	boxWidth := lipgloss.Width(content) + 2*styles.SpaceM
	if fw := lipgloss.Width(filterBar) + 2*styles.SpaceM; fw > boxWidth {
		boxWidth = fw
	}

	return styles.PrimaryBoxStyle.
		Width(boxWidth).
		Render(content)
}

// SelectedItem returns the currently selected service
func (m HomeMenuModel) SelectedItem() ServiceItem {
	if m.narrowMode {
		selectable := m.selectableItems()
		if m.cursor < 0 || m.cursor >= len(selectable) {
			return ServiceItem{}
		}
		entry := m.filtered[selectable[m.cursor]]
		if entry.service != nil {
			return *entry.service
		}
		return ServiceItem{}
	}

	if m.onTopItem {
		if m.TopItem != nil {
			return *m.TopItem
		}
		return ServiceItem{}
	}
	if m.cursorCol < 0 || m.cursorCol >= len(m.columns) {
		return ServiceItem{}
	}
	rows := m.columns[m.cursorCol].rows
	if m.cursorRow < 0 || m.cursorRow >= len(rows) || rows[m.cursorRow].service == nil {
		return ServiceItem{}
	}
	return *rows[m.cursorRow].service
}

// IsOnCategory returns true if cursor is on a category header.
// With the new list, cursor never lands on headers.
func (m HomeMenuModel) IsOnCategory() bool {
	return false
}

// ToggleCurrentCategory is a no-op with the new flat list.
func (m *HomeMenuModel) ToggleCurrentCategory() {}

// FilterActive returns whether the filter input is currently active
func (m HomeMenuModel) FilterActive() bool {
	return m.filter.IsActive()
}

// HasFilter returns whether a non-empty filter value is applied
func (m HomeMenuModel) HasFilter() bool {
	return m.filter.Value() != ""
}

// UpdateViewportRows recalculates the number of visible rows from screen height.
// The menu box must stay compact enough that the banner, info box, hints, and
// status bar all remain visible on screen.
func (m *HomeMenuModel) UpdateViewportRows() {
	// External overhead (outside the menu box):
	//   banner: 6, gap: 1, info box: 3, gap: 1, gap: 1, hints: 1, version: 1, status bar: 1 = 15
	// Internal box overhead (borders, padding, title, filter):
	//   border: 2, padding: 2, title+gap: 2, filter+gap: 2, scroll indicators: 2 = 10
	const overhead = 25
	rows := m.ScreenHeight - overhead
	if rows < 5 {
		rows = 5
	}
	// Cap at 15 rows so the list stays compact and scrollable
	if rows > 15 {
		rows = 15
	}
	m.viewportRows = rows
	m.clampCursorAndScroll()
}

// UpdateViewportCols recalculates how many category columns fit the current
// terminal width, pairing with UpdateViewportRows' row-count-from-height
// formula. Below the width that fits even 2 columns, numCols computes to 1
// and rebuildColumns() sets widthNarrow (and therefore narrowMode) so the
// menu falls back to the flat list entirely -- there's no "1-column grid"
// mode, since at that point a grid is just a list with extra header rows.
func (m *HomeMenuModel) UpdateViewportCols() {
	const colGap = 4       // spacing rendered between adjacent columns
	const minColWidth = 20 // shortest a category column can render legibly
	const boxChrome = 2*styles.SpaceM + 2 // box padding + border, same budget convention as boxWidth below

	numCols := 1
	if available := m.ScreenWidth - boxChrome; available > 0 {
		numCols = (available + colGap) / (minColWidth + colGap)
	}
	if numCols < 1 {
		numCols = 1
	}
	// Cap at 4 -- beyond that, categories get cramped and left-to-right
	// eyeline scanning across more columns is the actual UX limit, not raw
	// width.
	if numCols > 4 {
		numCols = 4
	}
	m.numCols = numCols
	m.widthNarrow = numCols <= 1
	m.rebuildColumns()
}
