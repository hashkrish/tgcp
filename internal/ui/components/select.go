package components

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// SelectModel is a small modal list-picker: a fixed set of options navigated
// with up/down and picked with Enter, styled like Confirmation's modal box.
// It only tracks selection state and renders -- callers own the
// up/down/enter/esc keybinding wiring themselves, same convention as
// ConfirmationModel (see its "Services handle keybindings themselves" note).
type SelectModel struct {
	Title     string
	Options   []string // underlying values returned by Value()
	Labels    []string // display labels, parallel to Options; falls back to Options[i] if shorter or empty
	Selection int
}

// NewSelect creates a SelectModel over options, with the cursor starting on
// current (or the first option if current isn't found among options).
func NewSelect(title string, options []string, labels []string, current string) SelectModel {
	sel := 0
	for i, o := range options {
		if o == current {
			sel = i
			break
		}
	}
	return SelectModel{Title: title, Options: options, Labels: labels, Selection: sel}
}

// SelectNext moves the cursor to the next option, wrapping past the end.
func (m *SelectModel) SelectNext() {
	if len(m.Options) == 0 {
		return
	}
	m.Selection = (m.Selection + 1) % len(m.Options)
}

// SelectPrev moves the cursor to the previous option, wrapping before the start.
func (m *SelectModel) SelectPrev() {
	if len(m.Options) == 0 {
		return
	}
	m.Selection = (m.Selection - 1 + len(m.Options)) % len(m.Options)
}

// Value returns the currently-highlighted option's underlying value.
func (m SelectModel) Value() string {
	if m.Selection < 0 || m.Selection >= len(m.Options) {
		return ""
	}
	return m.Options[m.Selection]
}

func (m SelectModel) label(i int) string {
	if i < len(m.Labels) && m.Labels[i] != "" {
		return m.Labels[i]
	}
	return m.Options[i]
}

// View renders the picker as a centered modal over a screenWidth x
// screenHeight area, matching ConfirmationModel's OverlayBoxStyle framing.
func (m SelectModel) View(screenWidth, screenHeight int) string {
	title := lipgloss.NewStyle().Bold(true).Foreground(styles.ColorTextPrimary).Render(m.Title)

	rows := make([]string, len(m.Options))
	for i := range m.Options {
		label := m.label(i)
		if i == m.Selection {
			rows[i] = styles.SelectedActive.Render("› " + label)
		} else {
			rows[i] = styles.UnselectedItemStyle.Render("  " + label)
		}
	}

	hint := styles.SubtleStyle.Render("↑/↓ Move  Enter:Select  Esc:Cancel")

	parts := append([]string{title, ""}, rows...)
	parts = append(parts, "", hint)
	content := lipgloss.JoinVertical(lipgloss.Left, parts...)

	dialog := styles.OverlayBoxStyle.
		BorderForeground(styles.ColorBrandAccent).
		Padding(styles.SpaceS, styles.SpaceL).
		Width(clampDialogWidth(40, 4)).
		Render(content)

	return lipgloss.Place(screenWidth, screenHeight, lipgloss.Center, lipgloss.Center, dialog)
}
