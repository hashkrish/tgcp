package jobs

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) View() string {
	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		s.Name(),
		"History",
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
	j := s.selected
	if j == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		s.Name(),
		"History",
		j.Name,
	)

	rows := []components.KeyValue{
		{Key: "Time", Value: j.OccurredAt.Format("2006-01-02 15:04:05")},
		{Key: "Project", Value: j.ProjectID},
		{Key: "Service", Value: j.Service},
		{Key: "Resource", Value: j.Resource},
		{Key: "Name", Value: j.Name},
		{Key: "Action", Value: j.Action},
		{Key: "Status", Value: statusLabel(j.Status)},
	}
	if j.Error != "" {
		rows = append(rows, components.KeyValue{Key: "Error", Value: j.Error})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Job Details",
		Rows:       rows,
		FooterHint: "q Back",
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}
