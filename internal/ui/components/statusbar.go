package components

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

type StatusMsg string

type StatusBarModel struct {
	Message     string
	Mode        string // "NORMAL", "COMMAND", "FILTER"
	FocusPane   string // "HOME", "SIDEBAR", "MAIN"
	HelpText    string
	Width       int
	LastUpdated time.Time
	IsError     bool
}

func NewStatusBar() StatusBarModel {
	return StatusBarModel{
		Message:   "Ready",
		Mode:      "NORMAL",
		FocusPane: "",
		HelpText:  "", // Dynamically set by view
		Width:     80,
	}
}

// SetHelpText updates the help text
func (m *StatusBarModel) SetHelpText(text string) {
	m.HelpText = text
}

// SetFocusPane updates the active pane indicator
func (m *StatusBarModel) SetFocusPane(pane string) {
	m.FocusPane = pane
}

func (m StatusBarModel) Init() tea.Cmd {
	return nil
}

func (m StatusBarModel) Update(msg tea.Msg) (StatusBarModel, tea.Cmd) {
	switch msg := msg.(type) {
	case StatusMsg:
		m.Message = string(msg)
	}
	return m, nil
}

func (m StatusBarModel) View() string {
	// Separator character
	sep := lipgloss.NewStyle().
		Foreground(styles.ColorBorderSubtle).
		Render(" │ ")

	// Mode badge style
	modeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("232")).
		Background(styles.ColorBorderSubtle).
		Bold(true).
		Padding(0, 1)

	modeLabel := m.Mode
	if m.IsError {
		modeStyle = modeStyle.Background(styles.ColorError)
		modeLabel = "ERROR"
	} else if m.Mode == "COMMAND" {
		modeStyle = modeStyle.Background(styles.ColorBrandPrimary)
	} else if m.Mode == "FILTER" {
		// Filtering is informational, not a warning
		modeStyle = modeStyle.Background(styles.ColorInfo)
	} else if m.FocusPane == "MAIN" {
		modeStyle = modeStyle.Background(styles.ColorBrandAccent)
		modeLabel = m.FocusPane
	} else if m.FocusPane == "SIDEBAR" || m.FocusPane == "HOME" {
		modeStyle = modeStyle.Background(styles.ColorBorderSubtle)
		modeLabel = m.FocusPane
	} else if m.Mode == "NORMAL" {
		modeLabel = "NORMAL"
	}

	// Help hints style - format as [key] Action
	helpStyle := lipgloss.NewStyle().
		Foreground(styles.ColorTextMuted)

	// Layout: ┃ MODE ┃ Message ............... │ Help Hints
	mode := modeStyle.Render(modeLabel)

	// Right side: Help hints only (removed timestamp). Reserve a minimum
	// width for the message so a long HelpText can't push it to zero and
	// wrap the whole status bar off-screen on a narrow terminal.
	const minInfoWidth = 12
	sepWidth := lipgloss.Width(sep)
	helpText := m.HelpText
	if helpText != "" {
		maxHelpWidth := m.Width - lipgloss.Width(mode) - 1 - sepWidth - minInfoWidth
		if maxHelpWidth < 0 {
			maxHelpWidth = 0
		}
		if lipgloss.Width(helpText) > maxHelpWidth {
			helpText = truncateToWidth(helpText, maxHelpWidth)
		}
	}
	rightSide := ""
	if helpText != "" {
		rightSide = sep + helpStyle.Render(helpText)
	}

	// Calculate available width for message
	infoWidth := m.Width - lipgloss.Width(mode) - lipgloss.Width(rightSide) - 1
	if infoWidth < 0 {
		infoWidth = 0
	}

	message := m.Message
	if lipgloss.Width(message) > infoWidth {
		message = truncateToWidth(message, infoWidth)
	}
	info := styles.StatusBarStyle.Width(infoWidth).Render(message)

	return lipgloss.JoinHorizontal(lipgloss.Top, mode, " ", info, rightSide)
}

// truncateToWidth shortens s to fit within width columns, appending an
// ellipsis when truncated. Rune-safe so multi-byte characters aren't split.
func truncateToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(s)
	if width <= 3 {
		if width > len(runes) {
			width = len(runes)
		}
		return string(runes[:width])
	}
	for i := len(runes); i > 0; i-- {
		candidate := string(runes[:i]) + "..."
		if lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return "..."
}
