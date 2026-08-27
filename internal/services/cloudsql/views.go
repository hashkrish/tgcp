package cloudsql

import (
	"fmt"
	"strings"

	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderDetailView() string {
	if s.selectedInstance == nil {
		return "No instance selected"
	}
	i := s.selectedInstance

	doc := strings.Builder{}

	// Breadcrumb
	doc.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		i.Name,
	))
	doc.WriteString("\n\n")

	haStr := "No (ZONAL)"
	if i.AvailabilityType == "REGIONAL" {
		haStr = "Yes (REGIONAL)"
	}

	publicIPStr := "Disabled"
	if i.PublicIPEnabled {
		publicIPStr = "Enabled"
	}

	rows := []components.KeyValue{
		{Key: "Name", Value: i.Name},
		{Key: "State", Value: renderState(i.State)},
		{Key: "Database Version", Value: i.DatabaseVersion},
		{Key: "Region", Value: i.Region},
		{Key: "Zone", Value: i.Zone},
		{Key: "Tier", Value: i.Tier},
		{Key: "Storage (GB)", Value: fmt.Sprintf("%d", i.StorageGB)},
		{Key: "Disk Type", Value: i.DiskType},
		{Key: "High Availability", Value: haStr},
		{Key: "Auto Backup", Value: fmt.Sprintf("%v", i.AutoBackup)},
		{Key: "Activation Policy", Value: i.Activation},
		{Key: "Primary IP", Value: i.PrimaryIP},
		{Key: "Public IP", Value: publicIPStr},
		{Key: "Connection Name", Value: i.ConnectionName},
		{Key: "Maintenance Window", Value: renderMaintenanceWindow(i.MaintenanceDay, i.MaintenanceHour)},
	}

	if i.MasterInstanceName != "" {
		rows = append(rows, components.KeyValue{Key: "Master Instance", Value: i.MasterInstanceName})
	}
	if len(i.ReplicaNames) > 0 {
		rows = append(rows, components.KeyValue{Key: "Read Replicas", Value: strings.Join(i.ReplicaNames, ", ")})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Instance Details",
		Rows:       rows,
		FooterHint: "s Start | x Stop | q Back",
	})

	doc.WriteString(card)

	return doc.String()
}

var maintenanceDays = map[int64]string{
	1: "Monday", 2: "Tuesday", 3: "Wednesday", 4: "Thursday",
	5: "Friday", 6: "Saturday", 7: "Sunday",
}

// renderMaintenanceWindow formats the maintenance window day/hour into a readable string
func renderMaintenanceWindow(day, hour int64) string {
	if day == 0 {
		return "Any"
	}
	dayName, ok := maintenanceDays[day]
	if !ok {
		dayName = fmt.Sprintf("Day %d", day)
	}
	return fmt.Sprintf("%s %02d:00 UTC", dayName, hour)
}

func (s *Service) renderConfirmation() string {
	if s.selectedInstance == nil {
		return "Error: No instance selected"
	}

	return components.RenderConfirmation(s.pendingAction, s.selectedInstance.Name, "instance")
}

func renderState(state InstanceState) string {
	return components.RenderStatus(string(state))
}
