package disks

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders a confirmation dialog for pending disk actions
func (s *Service) renderConfirmation() string {
	if s.selectedDisk == nil {
		return "Error: No disk selected"
	}

	if s.pendingAction == "grant" {
		return components.RenderConfirmationWithMessage("grant", s.selectedDisk.Name, "disk",
			components.IAMConfirmMessage("disk", s.selectedDisk.Name, s.pendingIAMRole, s.pendingIAMMember))
	}
	if s.pendingAction == "start-replication" {
		return components.RenderConfirmationWithMessage("start-replication", s.selectedDisk.Name, "disk",
			fmt.Sprintf("Start async replication from disk %s to %s?",
				styles.TitleStyle.Render(s.selectedDisk.Name), s.pendingSecondary))
	}

	return components.RenderConfirmation(s.pendingAction, s.selectedDisk.Name, "disk")
}

// renderIAMView renders the current IAM policy bindings for the selected
// disk, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedDisk == nil {
		return "Error: No disk selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Disks",
		s.selectedDisk.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedDisk.Name, rows)
}

// formatLastAttach formats the disk's LastAttachTimestamp (RFC3339) for display.
func formatLastAttach(ts string) string {
	if ts == "" {
		return "Never"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02 15:04")
}

func (s *Service) renderDetailView() string {
	if s.selectedDisk == nil {
		return "No disk selected"
	}
	d := s.selectedDisk

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Disks",
		d.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Disk Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: d.Name},
			{Key: "Status", Value: components.RenderStatus(d.Status)},
			{Key: "Zone", Value: d.Zone},
			{Key: "Type", Value: d.ShortType()},
			{Key: "Size", Value: fmt.Sprintf("%d GB", d.SizeGb)},
			{Key: "Source Image", Value: d.SourceImage},
			{Key: "Last Attached", Value: formatLastAttach(d.LastAttachTimestamp)},
		},
	})

	// 3. Attachment Box
	var attachContent string
	if d.IsOrphan() {
		attachContent = styles.ErrorStyle.Render("🔴 ORPHAN: Not attached to any instance.")
	} else {
		var lines []string
		for _, u := range d.Users {
			parts := strings.Split(u, "/")
			instanceName := parts[len(parts)-1]
			lines = append(lines, fmt.Sprintf("• Instance: %s", instanceName))
		}
		attachContent = strings.Join(lines, "\n")
	}

	attachBox := components.DetailSection("Attachment", attachContent, styles.ColorBorderSubtle)

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		attachBox,
	)
}
