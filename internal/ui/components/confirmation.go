package components

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// ConfirmationModel represents a confirmation dialog component
type ConfirmationModel struct {
	Action       string // e.g., "start", "stop", "delete"
	ResourceName string // e.g., "prod-web-1"
	ResourceType string // e.g., "instance", "disk", "service"
	Message      string // Optional custom message (overrides default)
	Width        int
	Height       int
}

// NewConfirmationModel creates a new confirmation dialog
func NewConfirmationModel(action, resourceName, resourceType string) ConfirmationModel {
	return ConfirmationModel{
		Action:       action,
		ResourceName: resourceName,
		ResourceType: resourceType,
	}
}

// NewConfirmationModelWithMessage creates a confirmation with custom message
func NewConfirmationModelWithMessage(action, resourceName, resourceType, message string) ConfirmationModel {
	return ConfirmationModel{
		Action:       action,
		ResourceName: resourceName,
		ResourceType: resourceType,
		Message:      message,
	}
}

// Update handles messages (for future: interactive buttons, etc.)
func (m ConfirmationModel) Update(msg tea.Msg) (ConfirmationModel, tea.Cmd) {
	// For now, confirmation component is static
	// Services handle keybindings themselves
	return m, nil
}

// actionStyle defines the visual styling for different action types
type actionStyle struct {
	icon        string
	title       string
	borderColor lipgloss.Color
	titleStyle  lipgloss.Style
	impactText  string // Optional warning text for dangerous actions
}

// getActionStyle returns the appropriate styling for an action type
func getActionStyle(action string) actionStyle {
	switch action {
	case "delete", "remove", "destroy":
		// Dangerous actions - red, strong warning
		return actionStyle{
			icon:        "⚠",
			title:       "Confirm Deletion",
			borderColor: styles.ColorError,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorError).Bold(true),
			impactText:  "This action cannot be undone.",
		}
	case "stop", "terminate", "shutdown", "cancel", "drain", "disable", "pause", "detach", "suspend":
		// Disruptive actions - orange warning
		return actionStyle{
			icon:        "⏸",
			title:       "Confirm " + capitalize(action),
			borderColor: styles.ColorWarning,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true),
			impactText:  "",
		}
	case "purge":
		// Bulk-destructive but recoverable-by-nature action - red warning like delete
		return actionStyle{
			icon:        "⚠",
			title:       "Confirm Purge",
			borderColor: styles.ColorError,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorError).Bold(true),
			impactText:  "This immediately drops every task currently queued.",
		}
	case "start", "restart", "resume", "enable", "failover", "call", "run", "publish":
		// Safe/expected actions - blue/info
		return actionStyle{
			icon:        "▶",
			title:       "Confirm " + capitalize(action),
			borderColor: styles.ColorInfo,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorInfo).Bold(true),
			impactText:  "",
		}
	case "grant":
		// IAM policy writes - cautionary but not destructive; distinct
		// styling so it's never confused with delete/disable.
		return actionStyle{
			icon:        "🔑",
			title:       "Confirm IAM Grant",
			borderColor: styles.ColorWarning,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true),
			impactText:  "Double-check the role and member before confirming.",
		}
	case "snapshot", "backup":
		// Neutral actions - subtle
		return actionStyle{
			icon:        "◱",
			title:       "Confirm Snapshot",
			borderColor: styles.ColorBrandAccent,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorBrandAccent).Bold(true),
			impactText:  "",
		}
	default:
		// Default - orange warning (current behavior)
		return actionStyle{
			icon:        "⚠",
			title:       "Confirm Action",
			borderColor: styles.ColorWarning,
			titleStyle:  lipgloss.NewStyle().Foreground(styles.ColorWarning).Bold(true),
			impactText:  "",
		}
	}
}

// View renders the confirmation dialog
func (m ConfirmationModel) View() string {
	// Get action-specific styling
	style := getActionStyle(m.Action)

	// Title with icon
	title := style.titleStyle.Render(style.icon + " " + style.title)

	// Action text
	var actionText string
	if m.Message != "" {
		actionText = m.Message
	} else {
		actionText = m.buildActionText()
	}

	// Help text
	helpText := RenderFooterHint("y Confirm | n Cancel")

	// Build content parts
	parts := []string{title, "", actionText}

	// Add impact text for dangerous actions
	if style.impactText != "" {
		impactStyled := lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Italic(true).
			Render(style.impactText)
		parts = append(parts, "", impactStyled)
	}

	parts = append(parts, "", helpText)

	// Combine content
	content := lipgloss.JoinVertical(lipgloss.Center, parts...)

	// Wrap in styled box with action-specific border color
	dialog := styles.OverlayBoxStyle.
		BorderForeground(style.borderColor).
		Padding(styles.SpaceS, styles.SpaceL).
		Width(70).
		Render(content)

	// Center the dialog
	return lipgloss.Place(
		80, 20, // Approximate dimensions
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}

// buildActionText constructs the confirmation message based on action type
func (m ConfirmationModel) buildActionText() string {
	actionUpper := capitalize(m.Action)
	resourceNameStyled := styles.TitleStyle.Render(m.ResourceName)

	// Build action verb based on action type
	var verb string
	switch m.Action {
	case "start":
		verb = "START"
	case "stop":
		verb = "STOP"
	case "delete":
		verb = "DELETE"
	case "restart":
		verb = "RESTART"
	case "snapshot":
		verb = "CREATE SNAPSHOT OF"
	case "cancel":
		verb = "CANCEL"
	case "drain":
		verb = "DRAIN"
	case "disable":
		verb = "DISABLE"
	case "enable":
		verb = "ENABLE"
	case "pause":
		verb = "PAUSE"
	case "resume":
		verb = "RESUME"
	case "failover":
		verb = "FAILOVER"
	case "call":
		verb = "CALL"
	case "run":
		verb = "RUN"
	case "publish":
		verb = "PUBLISH TO"
	case "purge":
		verb = "PURGE"
	case "detach":
		verb = "DETACH"
	case "destroy":
		verb = "DESTROY"
	case "reset":
		verb = "RESET"
	case "suspend":
		verb = "SUSPEND"
	default:
		verb = actionUpper
	}

	return fmt.Sprintf(
		"Are you sure you want to %s %s %s?",
		verb,
		m.ResourceType,
		resourceNameStyled,
	)
}

// capitalize capitalizes the first letter of a string
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	// Convert first character to uppercase
	first := s[0]
	if first >= 'a' && first <= 'z' {
		first = first - 32
	}
	return string(first) + s[1:]
}

// SetSize updates the component size
func (m *ConfirmationModel) SetSize(width, height int) {
	m.Width = width
	m.Height = height
}

// RenderConfirmation is a convenience function for services to render confirmation dialogs
func RenderConfirmation(action, resourceName, resourceType string) string {
	model := NewConfirmationModel(action, resourceName, resourceType)
	return model.View()
}

// RenderConfirmationWithMessage renders a confirmation with a custom message
func RenderConfirmationWithMessage(action, resourceName, resourceType, message string) string {
	model := NewConfirmationModelWithMessage(action, resourceName, resourceType, message)
	return model.View()
}
