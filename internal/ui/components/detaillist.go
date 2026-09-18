package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// DetailList is a navigable key/value card: ↑/↓ move a cursor between rows so
// a single field can be highlighted and its value copied (see
// CopyToClipboardCmd), unlike the static DetailCard.
type DetailList struct {
	Title      string
	Rows       []KeyValue
	FooterHint string
	cursor     int
}

// NewDetailList creates a DetailList with the cursor on the first row.
func NewDetailList(title string, rows []KeyValue) DetailList {
	return DetailList{Title: title, Rows: rows}
}

// SetRows replaces the rows, clamping the cursor so it stays in range (e.g.
// when the underlying resource's field count changes between renders).
func (d *DetailList) SetRows(rows []KeyValue) {
	d.Rows = rows
	if d.cursor >= len(rows) {
		d.cursor = len(rows) - 1
	}
	if d.cursor < 0 {
		d.cursor = 0
	}
}

// CursorUp moves the highlighted row up, clamping at the top.
func (d *DetailList) CursorUp() {
	if d.cursor > 0 {
		d.cursor--
	}
}

// CursorDown moves the highlighted row down, clamping at the bottom.
func (d *DetailList) CursorDown() {
	if d.cursor < len(d.Rows)-1 {
		d.cursor++
	}
}

// Selected returns the currently highlighted row, and false if there are no
// rows to select.
func (d DetailList) Selected() (KeyValue, bool) {
	if d.cursor < 0 || d.cursor >= len(d.Rows) {
		return KeyValue{}, false
	}
	return d.Rows[d.cursor], true
}

// View renders the card with the selected row highlighted.
func (d DetailList) View() string {
	width := detailCardWidth()

	title := styles.HeaderStyle.Width(width).Render(d.Title)

	maxKeyLen := 0
	for _, row := range d.Rows {
		if len(row.Key) > maxKeyLen {
			maxKeyLen = len(row.Key)
		}
	}

	// Extra breathing room beyond the longest key, so the label column
	// doesn't sit flush against the value column.
	keyColWidth := maxKeyLen + 3

	cursorStyle := lipgloss.NewStyle().Foreground(styles.ColorBrandAccent).Bold(true)
	keyStyle := lipgloss.NewStyle().Foreground(styles.ColorBrandAccent).Bold(true)
	labelStyle := styles.LabelStyle.Width(keyColWidth)
	cursorKeyStyle := cursorStyle.Width(keyColWidth)

	lines := make([]string, 0, len(d.Rows))
	for idx, row := range d.Rows {
		key := row.Key + ":"
		value := row.Value

		selected := idx == d.cursor
		if statusKeys[row.Key] && !strings.Contains(row.Value, "\x1b") {
			value = RenderStatus(row.Value)
		} else if row.UseValueStyle || !strings.Contains(row.Value, "\x1b") {
			if selected {
				value = keyStyle.Render(value)
			} else {
				value = styles.ValueStyle.Render(value)
			}
		}

		marker := "  "
		renderedKey := labelStyle.Render(key)
		if selected {
			marker = cursorStyle.Render("▸ ")
			renderedKey = cursorKeyStyle.Render(key)
		}

		lines = append(lines, fmt.Sprintf("%s%s %s", marker, renderedKey, value))
	}

	box := styles.PrimaryBoxStyle.
		BorderForeground(styles.ColorBorderSubtle).
		BorderLeft(false).
		BorderRight(false).
		Width(width).
		Render(strings.Join(lines, "\n"))

	parts := []string{title, box}
	if d.FooterHint != "" {
		parts = append(parts, RenderFooterHint(d.FooterHint))
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
