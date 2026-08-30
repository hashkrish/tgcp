package firestore

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the database delete/bulk-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedDB == nil {
		return "Error: No database selected"
	}
	if s.pendingAction == "bulk-delete" {
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedDB.Name,
			"database",
			fmt.Sprintf("Are you sure you want to bulk-delete EVERY document in %s? This does not delete the database itself, only its data, and cannot be undone.", s.selectedDB.Name),
		)
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedDB.Name,
		"database",
		fmt.Sprintf("Are you sure you want to DELETE database %s? This permanently deletes all its data. Databases with delete protection enabled cannot be deleted until protection is disabled (u:Update).", s.selectedDB.Name),
	)
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Firestore", "Databases")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewDetail:
		return s.renderDetailView()
	case ViewNamespaces:
		return s.renderNamespacesView()
	case ViewKinds:
		return s.renderKindsView()
	case ViewCreate:
		return s.createForm.View()
	case ViewUpdate:
		return s.updateForm.View()
	case ViewExport:
		return s.exportForm.View()
	case ViewImport:
		return s.importForm.View()
	case ViewClone:
		return s.cloneForm.View()
	case ViewRestore:
		return s.restoreForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	}

	// Default: List view
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Databases",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailView() string {
	db := s.selectedDB
	if db == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Databases",
		db.Name,
	)

	rows := []components.KeyValue{
		{Key: "Name", Value: db.Name},
		{Key: "Type", Value: strings.Replace(db.Type, "FIRESTORE_", "", 1)},
		{Key: "Location", Value: db.Location},
		{Key: "Created", Value: db.CreateTime},
		{Key: "UID", Value: db.Uid},
	}
	if db.UpdateTime != "" {
		rows = append(rows, components.KeyValue{Key: "Updated", Value: db.UpdateTime})
	}
	if db.DatabaseEdition != "" {
		rows = append(rows, components.KeyValue{Key: "Edition", Value: db.DatabaseEdition})
	}
	if db.ConcurrencyMode != "" {
		rows = append(rows, components.KeyValue{Key: "Concurrency Mode", Value: db.ConcurrencyMode})
	}
	if db.DeleteProtectionState != "" {
		rows = append(rows, components.KeyValue{Key: "Delete Protection", Value: db.DeleteProtectionState})
	}
	if db.PointInTimeRecoveryEnablement != "" {
		rows = append(rows, components.KeyValue{Key: "Point-in-Time Recovery", Value: db.PointInTimeRecoveryEnablement})
	}
	rows = append(rows, components.KeyValue{Key: "Free Tier", Value: fmt.Sprintf("%t", db.FreeTier)})

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Database Details",
		Rows:  rows,
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

func (s *Service) renderNamespacesView() string {
	if s.selectedDB == nil {
		return ""
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		s.selectedDB.Name+" (Datastore)",
		"Namespaces",
	))
	content.WriteString("\n\n")
	content.WriteString(s.nsTable.View())
	return content.String()
}

func (s *Service) renderKindsView() string {
	if s.selectedDB == nil || s.selectedNamespace == nil {
		return ""
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		s.selectedDB.Name+" (Datastore)",
		s.selectedNamespace.Name,
		"Kinds",
	))
	content.WriteString("\n\n")
	content.WriteString(s.kindTable.View())
	return content.String()
}
