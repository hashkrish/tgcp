package dataproc

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the cluster-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedCluster == nil {
		return "Error: No cluster selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedCluster.Name, "cluster")
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Dataproc", "Clusters")
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
		"Clusters",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.clusters) == 0 {
		content.WriteString(components.EmptyState("clusters"))
		return content.String()
	}
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailView() string {
	c := s.selectedCluster
	if c == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Clusters",
		c.Name,
	)

	workers := fmt.Sprintf("%d x %s", c.WorkerCount, c.WorkerMachine)

	rows := []components.KeyValue{
		{Key: "Name", Value: c.Name},
		{Key: "Status", Value: components.RenderStatus(c.Status)},
		{Key: "Region", Value: DefaultRegion},
		{Key: "Zone", Value: c.Zone},
		{Key: "Master", Value: c.MasterMachine},
		{Key: "Workers", Value: workers},
	}
	if c.StatusDetail != "" {
		rows = append(rows, components.KeyValue{Key: "Status Detail", Value: c.StatusDetail})
	}
	if c.StateStartTime != "" {
		rows = append(rows, components.KeyValue{Key: "State Since", Value: c.StateStartTime})
	}
	if c.ClusterUUID != "" {
		rows = append(rows, components.KeyValue{Key: "Cluster UUID", Value: c.ClusterUUID})
	}
	if c.ConfigBucket != "" {
		rows = append(rows, components.KeyValue{Key: "Config Bucket", Value: c.ConfigBucket})
	}
	if len(c.Labels) > 0 {
		rows = append(rows, components.KeyValue{Key: "Labels", Value: formatLabels(c.Labels)})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Cluster Details",
		Rows:  rows,
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

func formatLabels(labels map[string]string) string {
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
