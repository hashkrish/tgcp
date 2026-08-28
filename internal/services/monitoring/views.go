package monitoring

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Monitoring")
	}

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
	return s.renderListView()
}

// renderConfirmation renders the uptime-check/alert-policy delete
// confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.activeTab == TabUptimeChecks {
		if s.selectedCheck == nil {
			return "Error: No uptime check selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedCheck.DisplayName, "uptime check")
	}
	if s.selectedAlert == nil {
		return "Error: No alert policy selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedAlert.DisplayName, "alert policy")
}

func (s *Service) renderListView() string {
	label := "Uptime Checks"
	if s.activeTab == TabAlertPolicies {
		label = "Alert Policies"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		label,
	)

	var uStyle, aStyle lipgloss.Style
	if s.activeTab == TabUptimeChecks {
		uStyle = styles.ActiveTabStyle
		aStyle = styles.InactiveTabStyle
	} else {
		uStyle = styles.InactiveTabStyle
		aStyle = styles.ActiveTabStyle
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top,
		uStyle.Render("Uptime Checks"),
		aStyle.Render("Alert Policies"),
	)

	var content string
	if s.activeTab == TabUptimeChecks {
		if len(s.uptimeChecks) == 0 {
			content = components.EmptyState("uptime checks")
		} else {
			content = s.uptimeTable.View()
		}
	} else {
		if len(s.alertPolicys) == 0 {
			content = components.EmptyState("alert policies")
		} else {
			content = s.alertTable.View()
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, content)
}

func (s *Service) renderDetailView() string {
	if s.activeTab == TabUptimeChecks {
		return s.renderUptimeDetail()
	}
	return s.renderAlertDetail()
}

func (s *Service) renderUptimeDetail() string {
	c := s.selectedCheck
	if c == nil {
		return "No Uptime check selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Uptime Checks",
		c.DisplayName,
	)

	target := c.Host
	if c.CheckType != "TCP" && c.Path != "" {
		target = fmt.Sprintf("%s%s", c.Host, c.Path)
	}
	port := ""
	if c.Port != 0 {
		port = fmt.Sprintf("%d", c.Port)
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Uptime Check Details",
		Rows: []components.KeyValue{
			{Key: "Display Name", Value: c.DisplayName},
			{Key: "Resource Type", Value: c.ResourceType},
			{Key: "Check Type", Value: c.CheckType},
			{Key: "Host", Value: c.Host},
			{Key: "Target", Value: target},
			{Key: "Port", Value: port},
			{Key: "Period", Value: c.Period},
			{Key: "Timeout", Value: c.Timeout},
		},
	})

	sections := []string{breadcrumb, "", card}

	if len(c.ContentMatchers) > 0 {
		rows := make([]components.KeyValue, len(c.ContentMatchers))
		for i, cm := range c.ContentMatchers {
			rows[i] = components.KeyValue{Key: cm.Matcher, Value: cm.Content}
		}
		matchers := components.DetailCard(components.DetailCardOpts{
			Title: "Content Matchers",
			Rows:  rows,
		})
		sections = append(sections, "", matchers)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (s *Service) renderAlertDetail() string {
	p := s.selectedAlert
	if p == nil {
		return "No alert policy selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Alert Policies",
		p.DisplayName,
	)

	enabled := "false"
	if p.Enabled {
		enabled = "true"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Policy Details",
		Rows: []components.KeyValue{
			{Key: "Display Name", Value: p.DisplayName},
			{Key: "Enabled", Value: enabled},
			{Key: "Combiner", Value: p.Combiner},
			{Key: "Conditions", Value: fmt.Sprintf("%d", len(p.Conditions))},
			// Notification channels: count only. Channel details (email/SMS/
			// phone contact info) are never fetched or rendered here.
			{Key: "Notification Channels", Value: fmt.Sprintf("%d", p.NotificationChannelCount)},
		},
	})

	sections := []string{breadcrumb, "", card}

	for i, cond := range p.Conditions {
		rows := []components.KeyValue{
			{Key: "Type", Value: cond.Kind},
		}
		if cond.MetricFilter != "" {
			rows = append(rows, components.KeyValue{Key: "Metric Filter", Value: cond.MetricFilter})
		}
		if cond.Comparison != "" {
			rows = append(rows, components.KeyValue{Key: "Comparison", Value: fmt.Sprintf("%s %s", cond.Comparison, cond.Threshold)})
		}
		if cond.Duration != "" {
			rows = append(rows, components.KeyValue{Key: "Duration", Value: cond.Duration})
		}

		title := cond.DisplayName
		if title == "" {
			title = fmt.Sprintf("Condition %d", i+1)
		}
		condCard := components.DetailCard(components.DetailCardOpts{
			Title: title,
			Rows:  rows,
		})
		sections = append(sections, "", condCard)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}
