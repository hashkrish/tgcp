package logging

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// View renders the service UI
func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Logs")
	}

	// Show animated spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.resourcesMode {
		return s.renderResourcesView()
	}

	if s.viewingDetail {
		return s.renderDetailView()
	}

	// Default: List View
	return s.renderListView()
}

// renderResourcesView renders the sinks/metrics/buckets/views browser.
func (s *Service) renderResourcesView() string {
	switch s.resourceViewState {
	case ResourceViewCreate, ResourceViewGrantIAM:
		return s.resourceCreateForm.View()
	case ResourceViewConfirmation:
		return components.RenderConfirmation("delete", s.pendingResourceDelete, "resource")
	}

	var label string
	switch s.resourceTab {
	case ResourceTabMetrics:
		label = "Log Metrics"
	case ResourceTabBuckets:
		label = "Log Buckets"
	case ResourceTabViews:
		label = fmt.Sprintf("Views (bucket %s)", s.viewBucketID)
	default:
		label = "Sinks"
	}

	breadcrumb := components.Breadcrumb(s.Name(), "Resources", label)

	tabStyle := func(t ResourceTab) lipgloss.Style {
		if s.resourceTab == t {
			return styles.ActiveTabStyle
		}
		return styles.InactiveTabStyle
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top,
		tabStyle(ResourceTabSinks).Render(" Sinks "),
		tabStyle(ResourceTabMetrics).Render(" Metrics "),
		tabStyle(ResourceTabBuckets).Render(" Buckets "),
		tabStyle(ResourceTabViews).Render(" Views "),
	)

	var content string
	switch s.resourceTab {
	case ResourceTabSinks:
		if len(s.sinks) == 0 {
			content = components.EmptyState("sinks")
		} else {
			content = s.sinkTable.View()
		}
	case ResourceTabMetrics:
		if len(s.metrics) == 0 {
			content = components.EmptyState("log metrics")
		} else {
			content = s.metricTable.View()
		}
	case ResourceTabBuckets:
		if len(s.buckets) == 0 {
			content = components.EmptyState("log buckets")
		} else {
			content = s.bucketTable.View()
		}
	case ResourceTabViews:
		if len(s.views) == 0 {
			content = components.EmptyState("views")
		} else {
			content = s.viewTable.View()
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, content)
}

// renderListView renders the compact log table
func (s *Service) renderListView() string {
	// Breadcrumb
	var crumbItems []string
	if s.heading != "" {
		crumbItems = []string{s.Name(), s.heading}
	} else {
		crumbItems = []string{s.Name(), "All Logs"}
	}

	breadcrumb := components.Breadcrumb(crumbItems...)

	// Page indicator
	pageInfo := ""
	if len(s.tokenStack) > 0 || s.nextPageToken != "" {
		page := len(s.tokenStack) + 1
		pageInfo = fmt.Sprintf("  Page %d", page)
		if s.nextPageToken != "" {
			pageInfo += "+"
		}
	}

	// Count indicator
	countInfo := fmt.Sprintf("(%d entries%s)", len(s.entries), pageInfo)
	countStyle := lipgloss.NewStyle().Foreground(styles.ColorTextMuted)

	header := lipgloss.JoinHorizontal(lipgloss.Left,
		breadcrumb,
		"  ",
		countStyle.Render(countInfo),
	)

	content := s.table.View()
	if len(s.entries) == 0 {
		content = components.EmptyState("logs")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		"",
		content,
	)
}

// renderDetailView shows full log entry details
func (s *Service) renderDetailView() string {
	if s.selectedEntry == nil {
		return "No entry selected"
	}

	e := s.selectedEntry

	breadcrumb := components.Breadcrumb(s.Name(), "Entry Detail")

	// Format the full payload with word wrap
	wrapWidth := s.width - 10
	if wrapWidth < 40 {
		wrapWidth = 80
	}

	// Metadata section
	metaRows := []components.KeyValue{
		{Key: "Timestamp", Value: e.Timestamp.Local().Format("2006-01-02 15:04:05.000")},
		{Key: "Severity", Value: formatSeverityShort(e.Severity)},
		{Key: "Resource", Value: fmt.Sprintf("%s / %s", e.ResourceType, e.ResourceName)},
		{Key: "Location", Value: e.Location},
		{Key: "Log Name", Value: e.LogName},
		{Key: "Insert ID", Value: e.InsertID},
	}
	if e.ProjectID != "" {
		metaRows = append(metaRows, components.KeyValue{Key: "Project", Value: e.ProjectID})
	}

	metaCard := components.DetailCard(components.DetailCardOpts{
		Title: "Log Entry Metadata",
		Rows:  metaRows,
	})

	// Payload section with full content
	payloadStyle := lipgloss.NewStyle().
		Foreground(styles.ColorTextPrimary).
		Width(wrapWidth)

	payloadBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.ColorBorderSubtle).
		Padding(1).
		Width(wrapWidth + 4)

	payloadContent := payloadStyle.Render(e.Payload)
	if e.FullPayload != "" && e.FullPayload != e.Payload {
		payloadContent = payloadStyle.Render(e.FullPayload)
	}

	payloadSection := payloadBox.Render(payloadContent)

	// Labels if present
	labelsSection := ""
	if len(e.Labels) > 0 {
		var labelLines []string
		for k, v := range e.Labels {
			labelLines = append(labelLines, fmt.Sprintf("  %s: %s", k, v))
		}
		labelsSection = "\n" + lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Render("Labels:\n"+strings.Join(labelLines, "\n"))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		metaCard,
		"",
		"Message:",
		payloadSection,
		labelsSection,
	)
}
