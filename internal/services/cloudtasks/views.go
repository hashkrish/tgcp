package cloudtasks

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderDetailView() string {
	if s.selectedQueue == nil {
		return "No queue selected"
	}
	q := s.selectedQueue

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Queues",
		q.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Queue Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: q.Name},
			{Key: "State", Value: components.RenderStatus(q.State)},
			{Key: "Region", Value: q.Location},
		},
	})

	retryDur := q.MaxRetryDuration
	if retryDur == "" {
		retryDur = "Unlimited"
	}
	rate := components.DetailCard(components.DetailCardOpts{
		Title: "Rate Limits & Retry",
		Rows: []components.KeyValue{
			{Key: "Max Dispatch Rate", Value: fmt.Sprintf("%.1f tasks/sec", q.MaxDispatchRate)},
			{Key: "Max Concurrent Dispatches", Value: fmt.Sprintf("%d", q.MaxConcurrent)},
			{Key: "Max Attempts", Value: fmt.Sprintf("%d", q.MaxAttempts)},
			{Key: "Max Retry Duration", Value: retryDur},
		},
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		rate,
	)
}
