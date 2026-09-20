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
	Width       int
	LastUpdated time.Time
	IsError     bool
}

func NewStatusBar() StatusBarModel {
	return StatusBarModel{
		Message:   "Ready",
		Mode:      "NORMAL",
		FocusPane: "",
		Width:     80,
	}
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

	// Layout: ┃ MODE ┃ Message
	mode := modeStyle.Render(modeLabel)

	// Calculate available width for message
	infoWidth := max(m.Width-lipgloss.Width(mode)-1, 0)

	message := m.Message
	if lipgloss.Width(message) > infoWidth {
		message = truncateToWidth(message, infoWidth)
	}
	info := styles.StatusBarStyle.Width(infoWidth).Render(message)

	return lipgloss.JoinHorizontal(lipgloss.Top, mode, " ", info)
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
