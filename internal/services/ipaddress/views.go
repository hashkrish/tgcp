package ipaddress

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Addresses")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	return s.renderListView()
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Addresses",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")

	if len(s.addresses) == 0 {
		content.WriteString(components.EmptyState("addresses"))
		return content.String()
	}

	content.WriteString(s.table.View())
	return content.String()
}

// renderConfirmation renders a confirmation dialog for the pending
// release-address action.
func (s *Service) renderConfirmation() string {
	if s.selectedAddress == nil {
		return "Error: No address selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedAddress.Name, "address")
}

// formatCreated formats the address's CreationTimestamp (RFC3339) for display.
func formatCreated(ts string) string {
	if ts == "" {
		return "-"
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return ts
	}
	return t.Format("2006-01-02 15:04")
}

func emptyDash(v string) string {
	if v == "" {
		return "-"
	}
	return v
}

func (s *Service) renderDetailView() string {
	if s.selectedAddress == nil {
		return "No address selected"
	}
	a := s.selectedAddress

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Addresses",
		a.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Address Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: a.Name},
			{Key: "Address", Value: a.Address},
			{Key: "Status", Value: components.RenderStatus(a.Status)},
			{Key: "Scope", Value: a.Scope()},
			{Key: "Type", Value: a.AddressType},
			{Key: "IP Version", Value: emptyDash(a.IPVersion)},
			{Key: "Network Tier", Value: emptyDash(a.NetworkTier)},
			{Key: "Purpose", Value: emptyDash(a.Purpose)},
			{Key: "Description", Value: emptyDash(a.Description)},
			{Key: "Created", Value: formatCreated(a.Created)},
		},
	})

	// In-use box, mirroring how Disks shows what's currently attached.
	var usersContent string
	if len(a.Users) == 0 {
		usersContent = styles.SubtleStyle.Render("Not currently in use by any resource.")
	} else {
		var lines []string
		for _, u := range a.Users {
			parts := strings.Split(u, "/")
			lines = append(lines, fmt.Sprintf("• %s", parts[len(parts)-1]))
		}
		usersContent = strings.Join(lines, "\n")
	}
	usersBox := components.DetailSection("In Use By", usersContent, styles.ColorBorderSubtle)

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		usersBox,
	)
}
