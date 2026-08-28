package iam

import (
	"fmt"
	"sort"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

func (s *Service) renderServiceAccountsList() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Service Accounts",
	)
	if len(s.accounts) == 0 {
		return lipgloss.JoinVertical(
			lipgloss.Left,
			breadcrumb,
			components.EmptyState("services"),
		)
	}
	return lipgloss.JoinVertical(
		lipgloss.Left,
		breadcrumb,
		s.table.View(),
	)
}

func (s *Service) renderDetailView() string {
	if s.selectedAccount == nil {
		return "No account selected"
	}

	// Breadcrumb
	header := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Service Accounts",
		s.selectedAccount.DisplayName,
	)

	sections := []string{
		header,
		"",
		components.DetailCard(components.DetailCardOpts{
			Title: "Service Account Details",
			Rows: []components.KeyValue{
				{Key: "Display Name", Value: s.selectedAccount.DisplayName},
				{Key: "Email", Value: s.selectedAccount.Email},
				{Key: "Unique ID", Value: s.selectedAccount.UniqueID},
				{Key: "Status", Value: activeStatus(s.selectedAccount.Disabled)},
				{Key: "Description", Value: s.selectedAccount.Description},
				{Key: "Resource Name", Value: s.selectedAccount.Name},
			},
		}),
	}

	if roles := s.rolesForSelectedAccount(); len(roles) > 0 {
		rows := make([]components.KeyValue, len(roles))
		for i, role := range roles {
			rows[i] = components.KeyValue{Key: fmt.Sprintf("%d", i+1), Value: role}
		}
		sections = append(sections, "", components.DetailCard(components.DetailCardOpts{
			Title: "IAM Roles",
			Rows:  rows,
		}))
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// rolesForSelectedAccount returns the sorted, deduplicated list of project
// roles granted to the selected service account, derived from the
// project-wide IAM policy fetched once and shared across all accounts.
func (s *Service) rolesForSelectedAccount() []string {
	if s.selectedAccount == nil {
		return nil
	}
	member := "serviceAccount:" + s.selectedAccount.Email
	seen := make(map[string]bool)
	var roles []string
	for _, b := range s.policyBindings {
		if b.Member == member && !seen[b.Role] {
			seen[b.Role] = true
			roles = append(roles, b.Role)
		}
	}
	sort.Strings(roles)
	return roles
}

func activeStatus(disabled bool) string {
	if disabled {
		return lipgloss.NewStyle().Foreground(styles.ColorError).Render("Disabled")
	}
	return lipgloss.NewStyle().Foreground(styles.ColorSuccess).Render("Active")
}
