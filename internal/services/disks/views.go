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

	return components.RenderConfirmation(s.pendingAction, s.selectedDisk.Name, "disk")
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
