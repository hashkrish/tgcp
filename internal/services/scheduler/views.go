package scheduler

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderDetailView() string {
	if s.selectedJob == nil {
		return "No job selected"
	}
	j := s.selectedJob

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Jobs",
		j.Name,
	)

	nextRun := j.ScheduleTime
	if nextRun == "" {
		nextRun = "N/A"
	}
	lastRun := j.LastAttempt
	if lastRun == "" {
		lastRun = "Never"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Job Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: j.Name},
			{Key: "State", Value: components.RenderStatus(j.State)},
			{Key: "Region", Value: j.Location},
			{Key: "Schedule (cron)", Value: j.Schedule},
			{Key: "Time Zone", Value: j.TimeZone},
			{Key: "Next Run", Value: nextRun},
			{Key: "Last Attempt", Value: lastRun},
		},
	})

	target := components.DetailCard(components.DetailCardOpts{
		Title: "Target",
		Rows: []components.KeyValue{
			{Key: "Type", Value: j.TargetType},
			{Key: "Destination", Value: j.TargetSummary},
		},
	})

	retryDur := j.MaxRetryDur
	if retryDur == "" {
		retryDur = "Unlimited"
	}
	retry := components.DetailCard(components.DetailCardOpts{
		Title: "Retry Config",
		Rows: []components.KeyValue{
			{Key: "Retry Count", Value: fmt.Sprintf("%d", j.RetryCount)},
			{Key: "Max Retry Duration", Value: retryDur},
		},
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		target,
		"",
		retry,
	)
}
