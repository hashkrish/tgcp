package redis

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
	return components.RenderConfirmation(s.pendingAction, s.selectedInstance.Name, "instance")
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Redis", "Instances")
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

	rows := []components.KeyValue{
		{Key: "Name", Value: i.Name},
		{Key: "Status", Value: components.RenderStatus(i.State)},
		{Key: "Status Message", Value: emptyDash(i.StatusMessage)},
		{Key: "Display Name", Value: i.DisplayName},
		{Key: "Location", Value: i.Location},
		{Key: "Zone", Value: emptyDash(i.LocationID)},
		{Key: "Current Zone", Value: emptyDash(i.CurrentLocationID)},
		{Key: "Tier", Value: i.Tier},
		{Key: "Capacity", Value: fmt.Sprintf("%d GB", i.MemorySizeGb)},
		{Key: "Version", Value: i.RedisVersion},
		{Key: "Host", Value: i.Host},
		{Key: "Port", Value: fmt.Sprintf("%d", i.Port)},
	}
	if i.ReadEndpoint != "" {
		rows = append(rows, components.KeyValue{Key: "Read Endpoint", Value: fmt.Sprintf("%s:%d", i.ReadEndpoint, i.ReadEndpointPort)})
	}
	rows = append(rows,
		components.KeyValue{Key: "Network", Value: i.AuthorizedNetwork},
		components.KeyValue{Key: "Connect Mode", Value: emptyDash(i.ConnectMode)},
		components.KeyValue{Key: "Reserved IP Range", Value: emptyDash(i.ReservedIPRange)},
		components.KeyValue{Key: "Transit Encryption", Value: emptyDash(i.TransitEncryption)},
		components.KeyValue{Key: "AUTH Enabled", Value: formatBool(i.AuthEnabled)},
		components.KeyValue{Key: "Replica Count", Value: fmt.Sprintf("%d", i.ReplicaCount)},
		components.KeyValue{Key: "Read Replicas", Value: emptyDash(i.ReadReplicasMode)},
		components.KeyValue{Key: "Persistence", Value: emptyDash(i.PersistenceMode)},
	)
	if i.CustomerManagedKey != "" {
		rows = append(rows, components.KeyValue{Key: "CMEK", Value: i.CustomerManagedKey})
	}
	rows = append(rows,
		components.KeyValue{Key: "Created", Value: emptyDash(i.CreateTime)},
		components.KeyValue{Key: "Labels", Value: formatLabels(i.Labels)},
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Instance Details",
		Rows:  rows,
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatBool(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

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
