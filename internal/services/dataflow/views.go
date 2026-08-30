package dataflow

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the job-archive/cancel/drain confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedJob == nil {
		return "Error: No job selected"
	}
	if s.pendingAction == "archive" {
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedJob.Name,
			"job",
			fmt.Sprintf("Archive job %s? Dataflow has no true delete — this sets the archived label, matching `gcloud dataflow jobs archive`. Only jobs already in a terminal state can be archived.", s.selectedJob.Name),
		)
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedJob.Name, "job")
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Dataflow", "Jobs")
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

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewUpdateOptions {
		return s.updateOptionsForm.View()
	}

	// Filter Bar
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Jobs",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.jobs) == 0 {
		content.WriteString(components.EmptyState("jobs"))
		return content.String()
	}
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailView() string {
	j := s.selectedJob
	if j == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Jobs",
		j.Name,
	)

	rows := []components.KeyValue{
		{Key: "Name", Value: j.Name},
		{Key: "ID", Value: j.ID},
		{Key: "Type", Value: strings.Replace(j.Type, "JOB_TYPE_", "", 1)},
		{Key: "State", Value: components.RenderStatus(j.State)},
		{Key: "Location", Value: j.Location},
		{Key: "Created", Value: j.CreateTime},
	}
	if j.StartTime != "" {
		rows = append(rows, components.KeyValue{Key: "Started", Value: j.StartTime})
	}
	if j.CurrentStateTime != "" {
		rows = append(rows, components.KeyValue{Key: "State Since", Value: j.CurrentStateTime})
	}
	if j.ReplacedByJobID != "" {
		rows = append(rows, components.KeyValue{Key: "Replaced By", Value: j.ReplacedByJobID})
	}
	card := components.DetailCard(components.DetailCardOpts{
		Title: "Job Details",
		Rows:  rows,
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}
