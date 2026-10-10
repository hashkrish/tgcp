package apis

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "APIs")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewDetail:
		return s.renderDetailView()
	case ViewEnableForm:
		return s.enableForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	}

	return s.renderListView()
}

func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "disable":
		return components.RenderConfirmationWithMessage("disable", s.pendingName, "API",
			"Resources and clients that rely on this API will stop working. If other enabled APIs depend on it, the request is rejected -- use X from the detail view to disable them too.")
	case "force-disable":
		return components.RenderConfirmationWithMessage("disable", s.pendingName, "API",
			"This also disables every enabled API that depends on it. Resources and clients that rely on any of them will stop working.")
	}
	return components.RenderConfirmation(s.pendingAction, s.pendingName, "API")
}

func (s *Service) renderListView() string {
	listLabel := "Enabled"
	tabs := lipgloss.JoinHorizontal(lipgloss.Top,
		styles.ActiveTabStyle.Render(" Enabled "),
		styles.InactiveTabStyle.Render(" Common "),
	)
	if s.activeTab == tabCommon {
		listLabel = "Common"
		tabs = lipgloss.JoinHorizontal(lipgloss.Top,
			styles.InactiveTabStyle.Render(" Enabled "),
			styles.ActiveTabStyle.Render(" Common "),
		)
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		listLabel,
	))
	content.WriteString("\n")
	content.WriteString(tabs)
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.currentItems()) == 0 {
		content.WriteString(components.EmptyState("apis"))
	} else {
		content.WriteString(s.table.View())
	}
	return content.String()
}

func (s *Service) renderDetailView() string {
	if s.selected == nil {
		return "No API selected"
	}
	a := s.selected

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		displayTitle(*a),
	)

	summary := a.Summary
	if summary == "" {
		summary = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "API Details",
		Rows: []components.KeyValue{
			{Key: "Title", Value: displayTitle(*a)},
			{Key: "Service", Value: a.Name},
			{Key: "State", Value: components.RenderStatus(a.State)},
			{Key: "Summary", Value: summary},
		},
	})

	hint := "e Enable | q Back"
	if a.State == "ENABLED" {
		hint = "x Disable | X Disable + Dependents | q Back"
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		components.RenderFooterHint(hint),
	)
}
