package gce

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderTabBar renders the Instances/Instance Groups tab strip. Previously
// there was no visible tab bar at all here -- only the status bar's
// "[]:Tabs" hint and the breadcrumb's trailing segment ("Instances" vs.
// "Instance Groups") indicated which tab was active, so switching tabs via
// '['/']' was effectively invisible unless a user already knew to look for
// it and blind-pressed the keys.
func (s *Service) renderTabBar() string {
	instancesStyle, groupsStyle := styles.InactiveTabStyle, styles.InactiveTabStyle
	if s.activeTab == TabInstanceGroups {
		groupsStyle = styles.ActiveTabStyle
	} else {
		instancesStyle = styles.ActiveTabStyle
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		instancesStyle.Render(" Instances "),
		groupsStyle.Render(" Instance Groups "),
	)
}

// instanceDetailRows builds the Instance Details rows shown in the detail
// view. Shared between rendering and the copy-on-select keybinding so both
// agree on exactly which value a given row copies.
func instanceDetailRows(i *Instance) []components.KeyValue {
	var totalDisk int64
	for _, d := range i.Disks {
		totalDisk += d.SizeGB
	}

	// Simple duration format: Xd Yh
	age := time.Since(i.CreationTime)
	days := int(age.Hours() / 24)
	ageStr := fmt.Sprintf("%d days ago", days)
	if days == 0 {
		hours := int(age.Hours())
		ageStr = fmt.Sprintf("%d hours ago", hours)
	}

	externalIP := i.ExternalIP
	if externalIP == "" {
		externalIP = "None"
	}

	tags := "None"
	if len(i.Tags) > 0 {
		tags = strings.Join(i.Tags, ", ")
	}

	rows := []components.KeyValue{
		{Key: "Name", Value: i.Name},
		{Key: "ID", Value: i.ID},
		{Key: "Status", Value: string(i.State)},
		{Key: "Zone", Value: i.Zone},
		{Key: "Machine Type", Value: i.MachineType},
		{Key: "OS Image", Value: i.OSImage},
		{Key: "Disk Size", Value: fmt.Sprintf("%d GB total (%d disks)", totalDisk, len(i.Disks))},
	}
	for _, d := range i.Disks {
		rows = append(rows, components.KeyValue{
			Key:   "  Disk: " + d.Name,
			Value: fmt.Sprintf("%d GB, %s", d.SizeGB, d.Type),
		})
	}
	rows = append(rows,
		components.KeyValue{Key: "Created", Value: ageStr},
		components.KeyValue{Key: "Estimated Cost", Value: EstimateCost(i.MachineType, i.Zone, i.Disks)},
		components.KeyValue{Key: "Internal IP", Value: i.InternalIP},
		components.KeyValue{Key: "External IP", Value: externalIP},
		components.KeyValue{Key: "Network Tags", Value: tags},
	)

	return rows
}

// renderDetailView renders the details of a single instance
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

	s.detailList.Title = "Instance Details"
	s.detailList.SetRows(instanceDetailRows(i))
	s.detailList.FooterHint = "↑↓ Select | y Copy | s Start | x Stop | R Reset | z Suspend | Z Resume | M Maintenance | h SSH | i IAM | u Update | d Delete | q Back"

	doc.WriteString(s.detailList.View())

	return doc.String()
}

// renderConfirmation renders a confirmation dialog
func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "delete-mig":
		if s.selectedGroup == nil {
			return "Error: No instance group selected"
		}
		return components.RenderConfirmation("delete", s.selectedGroup.Name, "instance group")
	case "mig-start":
		if s.selectedGroup == nil {
			return "Error: No instance group selected"
		}
		return components.RenderConfirmation("start", s.selectedGroup.Name, "instance group")
	case "mig-stop":
		if s.selectedGroup == nil {
			return "Error: No instance group selected"
		}
		return components.RenderConfirmation("stop", s.selectedGroup.Name, "instance group")
	case "mig-replace":
		if s.selectedGroup == nil {
			return "Error: No instance group selected"
		}
		return components.RenderConfirmationWithMessage("replace", s.selectedGroup.Name, "instance group",
			fmt.Sprintf("Recreate every instance in MIG %s?", styles.TitleStyle.Render(s.selectedGroup.Name)))
	case "mig-restart":
		if s.selectedGroup == nil {
			return "Error: No instance group selected"
		}
		return components.RenderConfirmation("restart", s.selectedGroup.Name, "instance group")
	case "grant":
		if s.selectedInstance == nil {
			return "Error: No instance selected"
		}
		return components.RenderConfirmationWithMessage("grant", s.selectedInstance.Name, "instance",
			components.IAMConfirmMessage("instance", s.selectedInstance.Name, s.pendingIAMRole, s.pendingIAMMember))
	}
	if s.selectedInstance == nil {
		return "Error: No instance selected"
	}

	return components.RenderConfirmation(s.pendingAction, s.selectedInstance.Name, "instance")
}

// renderIAMView renders the current IAM policy bindings for the selected
// VM instance, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedInstance == nil {
		return "Error: No instance selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		s.selectedInstance.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedInstance.Name, rows)
}

// renderListView renders the main list view, dispatching to whichever tab
// (Instances or Instance Groups) is currently active.
func (s *Service) renderListView() string {
	if s.activeTab == TabInstanceGroups {
		return s.renderGroupsView()
	}
	return s.renderInstancesView()
}

// renderInstancesView renders the instance table (existing behavior).
func (s *Service) renderInstancesView() string {
	doc := strings.Builder{}

	// Breadcrumb + Tabs + Filter Bar
	doc.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
	))
	doc.WriteString("\n")
	doc.WriteString(s.renderTabBar())
	doc.WriteString("\n")
	doc.WriteString(s.filter.View())
	doc.WriteString("\n")

	// Status summary pills above the table (only when we have data)
	if len(s.instances) == 0 {
		doc.WriteString(components.EmptyState("instances"))
		return doc.String()
	}

	states := make([]string, 0, len(s.instances))
	for _, i := range s.instances {
		states = append(states, string(i.State))
	}
	doc.WriteString(components.StatusSummary(states))
	doc.WriteString("\n\n")

	doc.WriteString(styles.BaseStyle.Render(s.table.View()))
	return doc.String()
}

// renderGroupsView renders the Instance Groups (MIGs) table.
func (s *Service) renderGroupsView() string {
	doc := strings.Builder{}

	doc.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instance Groups",
	))
	doc.WriteString("\n")
	doc.WriteString(s.renderTabBar())
	doc.WriteString("\n\n")

	if len(s.groups) == 0 {
		doc.WriteString(components.EmptyState("instance groups"))
		return doc.String()
	}

	doc.WriteString(styles.BaseStyle.Render(s.groupTable.View()))
	return doc.String()
}
