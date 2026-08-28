package bigtable

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
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
		fmt.Sprintf("Are you sure you want to DELETE instance %s? This destroys every cluster and table in it.", s.selectedInstance.Name),
	)
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Bigtable", "Instances")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewTables {
		return s.renderTablesView()
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

	if len(s.instances) == 0 {
		content.WriteString(components.EmptyState("databases"))
		return content.String()
	}

	states := make([]string, 0, len(s.instances))
	for _, i := range s.instances {
		states = append(states, i.State)
	}
	content.WriteString(components.StatusSummary(states))
	content.WriteString("\n\n")
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

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Instance Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: i.Name},
			{Key: "Status", Value: components.RenderStatus(i.State)},
			{Key: "Display Name", Value: i.DisplayName},
			{Key: "Type", Value: i.Type},
			{Key: "Edition", Value: i.Edition},
			{Key: "Project", Value: i.ProjectID},
			{Key: "Created", Value: i.CreateTime},
			{Key: "Labels", Value: formatLabels(i.Labels)},
		},
	})

	// Clusters
	clusterContent := components.InlineLoader("Loading clusters...")
	if s.clusters != nil {
		if len(s.clusters) == 0 {
			clusterContent = components.EmptyState("clusters")
		} else {
			var lines []string
			for _, c := range s.clusters {
				line := fmt.Sprintf(
					"• %s (%s): %d Nodes, %s [%s]",
					c.Name,
					c.Zone,
					c.ServeNodes,
					c.StorageType,
					c.State,
				)
				if c.AutoscalingMax > 0 {
					line += fmt.Sprintf(", Autoscaling %d-%d nodes @ %d%% CPU", c.AutoscalingMin, c.AutoscalingMax, c.AutoscalingCpuTarget)
				}
				if c.KmsKeyName != "" {
					line += fmt.Sprintf(", KMS Key: %s", c.KmsKeyName)
				}
				lines = append(lines, line)
			}
			clusterContent = strings.Join(lines, "\n")
		}
	}

	clusterBox := components.DetailSection("Clusters", clusterContent, styles.ColorBorderSubtle)

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		clusterBox,
	)
}

// renderTablesView renders the read-only data-plane tables list for the
// selected instance, analogous to Artifact Registry's repo->images
// drill-down.
func (s *Service) renderTablesView() string {
	i := s.selectedInstance
	if i == nil {
		return "Error: No instance selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		i.Name,
		"Tables",
	)

	var content strings.Builder
	content.WriteString(breadcrumb)
	content.WriteString("\n")
	content.WriteString(s.tablesFilter.View())
	content.WriteString("\n")

	if s.tables == nil {
		content.WriteString(components.InlineLoader("Loading tables..."))
		return content.String()
	}

	if len(s.tables) == 0 {
		content.WriteString(components.EmptyState("tables"))
		return content.String()
	}

	content.WriteString(s.tablesTable.View())
	return content.String()
}

func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, labels[k]))
	}
	return strings.Join(parts, ", ")
}
