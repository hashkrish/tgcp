package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// ErrorModel represents an error display component
type ErrorModel struct {
	Error       error
	Title       string   // e.g., "Error Loading Instances"
	ServiceName string   // e.g., "GCE"
	Suggestions []string // Helpful suggestions
	Width       int
	Height      int
}

// NewErrorModel creates a new error component
func NewErrorModel(err error, title, serviceName string) ErrorModel {
	return ErrorModel{
		Error:       err,
		Title:       title,
		ServiceName: serviceName,
		Suggestions: generateSuggestions(err, serviceName),
	}
}

// Update handles messages (for future: retry button, etc.)
func (m ErrorModel) Update(msg tea.Msg) (ErrorModel, tea.Cmd) {
	// For now, error component is static
	// Future: Add retry button, dismiss button, etc.
	return m, nil
}

// errorBoxMargin is how much terminal width the error box leaves unused on
// each side combined, so its border doesn't sit flush against the terminal
// edge. errorBoxMinWidth floors the box width on tiny terminals.
const (
	errorBoxMargin   = 6
	errorBoxMinWidth = 40
)

// errorBoxWidth sizes the box to almost the full terminal width (tracked via
// globalWidth, see termsize.go) rather than a fixed width -- a fixed 80-col
// box wasted most of a wide terminal and, worse, wrapped long GCP API error
// text (e.g. "SERVICE_DISABLED") into far more lines than necessary.
func errorBoxWidth() int {
	return max(globalWidth-errorBoxMargin, errorBoxMinWidth)
}

// errorBoxOverhead is the box chrome eaten out of errorBoxWidth before text
// can use it: 1-char border + Padding(SpaceS, SpaceM) horizontal padding on
// each side.
const errorBoxOverhead = 2 + 2*styles.SpaceM

// errorMaxMessageLines caps how many wrapped lines of the raw error text are
// shown. GCP API errors (e.g. "SERVICE_DISABLED") often bundle a long
// message plus repeated help links/metadata that, left unbounded, produce a
// box taller than the terminal -- since this box is rendered inline (not in
// a scrollable viewport), any overflow just pushes the top of the box off
// screen with no way to scroll back to it. Capping the line count keeps the
// whole box on screen; the full error is still visible in --debug logs.
const errorMaxMessageLines = 8

// View renders the error component
func (m ErrorModel) View() string {
	if m.Error == nil {
		return ""
	}

	// Error icon and title
	header := lipgloss.JoinHorizontal(
		lipgloss.Left,
		styles.ErrorStyle.Render("⚠ "),
		styles.ErrorStyle.Bold(true).Render(m.Title),
	)

	boxWidth := errorBoxWidth()
	contentWidth := boxWidth - errorBoxOverhead

	// Word-wrap the error message to the box's content width, then hard-cap
	// the number of lines so the box can never grow taller than fits on
	// screen (see errorMaxMessageLines).
	errorMsg := wrapAndCapMessage(m.Error.Error(), contentWidth, errorMaxMessageLines)

	// Suggestions section
	var suggestions string
	if len(m.Suggestions) > 0 {
		suggestionLines := make([]string, len(m.Suggestions))
		for i, suggestion := range m.Suggestions {
			suggestionLines[i] = fmt.Sprintf("  • %s", suggestion)
		}
		suggestions = lipgloss.JoinVertical(
			lipgloss.Left,
			styles.SubtleStyle.Render("▸ Suggestions:"),
			strings.Join(suggestionLines, "\n"),
		)
	}

	// Help text
	helpText := RenderFooterHint("r Retry | q Back")

	// Combine all parts
	content := lipgloss.JoinVertical(
		lipgloss.Left,
		header,
		"",
		styles.ValueStyle.Render("Failed: "+errorMsg),
		"",
		suggestions,
		"",
		helpText,
	)

	// Wrap in styled box
	box := styles.OverlayBoxStyle.
		BorderForeground(styles.ColorError).
		Padding(styles.SpaceS, styles.SpaceM).
		Width(boxWidth).
		Render(content)

	return box
}

// wrapAndCapMessage word-wraps msg to width, then truncates to at most
// maxLines, marking the cut with a trailing "…" so it's clear text was
// dropped rather than the error simply ending mid-thought.
func wrapAndCapMessage(msg string, width, maxLines int) string {
	wrapped := lipgloss.NewStyle().Width(width).Render(msg)
	lines := strings.Split(wrapped, "\n")
	if len(lines) <= maxLines {
		return wrapped
	}
	lines = lines[:maxLines]
	lines[maxLines-1] = strings.TrimRight(lines[maxLines-1], " ") + " …"
	return strings.Join(lines, "\n")
}

// generateSuggestions creates helpful suggestions based on error type
func generateSuggestions(err error, serviceName string) []string {
	errStr := err.Error()
	suggestions := []string{}

	// Permission errors
	if strings.Contains(errStr, "403") ||
		strings.Contains(errStr, "permission") ||
		strings.Contains(errStr, "Insufficient") ||
		strings.Contains(errStr, "denied") {
		suggestions = append(suggestions,
			fmt.Sprintf("Check IAM permissions for %s", serviceName),
			"Verify your account has the required roles",
		)
	}

	// Network errors
	if strings.Contains(errStr, "network") ||
		strings.Contains(errStr, "timeout") ||
		strings.Contains(errStr, "connection") ||
		strings.Contains(errStr, "dial") {
		suggestions = append(suggestions,
			"Check your internet connection",
			"Verify GCP API is accessible",
		)
	}

	// Not found errors
	if strings.Contains(errStr, "404") ||
		strings.Contains(errStr, "not found") ||
		strings.Contains(errStr, "does not exist") {
		suggestions = append(suggestions,
			"Verify the resource exists",
			"Check project ID is correct",
		)
	}

	// Rate limit errors
	if strings.Contains(errStr, "429") ||
		strings.Contains(errStr, "rate limit") ||
		strings.Contains(errStr, "quota") {
		suggestions = append(suggestions,
			"API rate limit reached",
			"Wait a moment and try again",
		)
	}

	// Authentication errors
	if strings.Contains(errStr, "401") ||
		strings.Contains(errStr, "unauthorized") ||
		strings.Contains(errStr, "credentials") ||
		strings.Contains(errStr, "authentication") {
		suggestions = append(suggestions,
			"Check your GCP credentials",
			"Run: gcloud auth application-default login",
		)
	}

	// Generic fallback
	if len(suggestions) == 0 {
		suggestions = append(suggestions,
			"Try refreshing with 'r'",
			"Check project configuration",
		)
	}

	return suggestions
}

// SetSize updates the component size
func (m *ErrorModel) SetSize(width, height int) {
	m.Width = width
	m.Height = height
}

// RenderError is a convenience function for services to render errors
func RenderError(err error, serviceName, resourceType string) string {
	title := fmt.Sprintf("Error Loading %s", resourceType)
	model := NewErrorModel(err, title, serviceName)
	return model.View()
}
