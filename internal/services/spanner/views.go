package spanner

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the instance-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedInstance == nil {
		return "Error: No instance selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedInstance.Name,
		"instance",
		fmt.Sprintf("Are you sure you want to DELETE instance %s? This destroys every database in it.", s.selectedInstance.Name),
	)
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Spanner", "Instances")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	// Filter Bar
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailView() string {
	i := s.selectedInstance
	if i == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		i.Name,
	)

	capacity := fmt.Sprintf("%d Nodes", i.NodeCount)
	if i.NodeCount == 0 {
		capacity = fmt.Sprintf("%d Processing Units", i.ProcessingUnits)
	}

	edition := i.Edition
	if edition == "" {
		edition = "-"
	}
	backupSchedule := i.DefaultBackupScheduleType
	if backupSchedule == "" {
		backupSchedule = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Instance Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: i.Name},
			{Key: "Status", Value: components.RenderStatus(i.State)},
			{Key: "Display Name", Value: i.DisplayName},
			{Key: "Configuration", Value: i.Config},
			{Key: "Capacity", Value: capacity},
			{Key: "Edition", Value: edition},
			{Key: "Default Backup Schedule", Value: backupSchedule},
			{Key: "Labels", Value: formatLabels(i.Labels)},
			{Key: "Created", Value: i.CreateTime},
			{Key: "Updated", Value: i.UpdateTime},
		},
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

// formatLabels renders a label map as a compact, single-line key=value list
// for display in a detail card row.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
