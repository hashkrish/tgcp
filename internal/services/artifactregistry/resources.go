package artifactregistry

import (
	"fmt"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// Message types for the Packages/Versions/Vulnerabilities sub-views.
type packagesMsg []PackageItem
type versionsMsg []VersionItem
type vulnMsg struct {
	counts []VulnerabilityCount
	err    error
}
type resourceActionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// newTagRemoveForm builds the FormModel for removing a single URL tag from
// a Docker image, seeded with its first tag if it has any.
func newTagRemoveForm(img DockerImage) components.FormModel {
	def := ""
	if len(img.Tags) > 0 {
		def = img.Tags[0]
	}
	return components.NewForm("Remove Tag: "+img.URI, []components.FormField{
		{Label: "Tag", Default: def, Required: true},
	})
}

// -----------------------------------------------------------------------------
// Fetch commands
// -----------------------------------------------------------------------------

func (s *Service) fetchPackagesCmd(repo RepositoryItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListPackages(s.repoFullName(repo))
		if err != nil {
			return errMsg(err)
		}
		return packagesMsg(items)
	}
}

func (s *Service) fetchVersionsCmd(pkg PackageItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListVersions(pkg.Name)
		if err != nil {
			return errMsg(err)
		}
		return versionsMsg(items)
	}
}

func (s *Service) fetchVulnCmd(img DockerImage) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return vulnMsg{err: fmt.Errorf("client not initialized")}
		}
		counts, err := s.client.GetVulnerabilitySummary(s.projectID, img.URI)
		return vulnMsg{counts: counts, err: err}
	}
}

// -----------------------------------------------------------------------------
// Action commands
// -----------------------------------------------------------------------------

func (s *Service) deleteTagCmd(repoFullName string, img DockerImage, tag string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteTag(repoFullName, img, tag); err != nil {
			return resourceActionResultMsg{err: err, resource: "tag", name: tag, action: "untag"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Removed tag %q", tag), resource: "tag", name: tag, action: "untag"}
	}
}

func (s *Service) deletePackageCmd(pkg PackageItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeletePackage(pkg.Name); err != nil {
			return resourceActionResultMsg{err: err, resource: "package", name: pkg.DisplayName, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted package %s", pkg.DisplayName), resource: "package", name: pkg.DisplayName, action: "delete"}
	}
}

func (s *Service) deleteVersionCmd(v VersionItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteVersion(v.Name); err != nil {
			return resourceActionResultMsg{err: err, resource: "version", name: v.Name, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted version %s", v.Name), resource: "version", name: v.Name, action: "delete"}
	}
}

// -----------------------------------------------------------------------------
// Table row builders
// -----------------------------------------------------------------------------

func packageRows(items []PackageItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, p := range items {
		created := ""
		if !p.CreateTime.IsZero() {
			created = p.CreateTime.Format("2006-01-02 15:04")
		}
		updated := ""
		if !p.UpdateTime.IsZero() {
			updated = p.UpdateTime.Format("2006-01-02 15:04")
		}
		rows = append(rows, table.Row{p.DisplayName, created, updated})
	}
	return rows
}

func versionRows(items []VersionItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, v := range items {
		created := ""
		if !v.CreateTime.IsZero() {
			created = v.CreateTime.Format("2006-01-02 15:04")
		}
		rows = append(rows, table.Row{v.Name, v.Description, created})
	}
	return rows
}

// -----------------------------------------------------------------------------
// Update handling
// -----------------------------------------------------------------------------

// handleResourceMsg processes the async load/action messages for the
// Packages/Versions/Vulnerabilities sub-views. Returns (model, cmd, handled).
func (s *Service) handleResourceMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch m := msg.(type) {
	case packagesMsg:
		s.packages = m
		s.packagesTable.SetRows(packageRows(m))
		return s, nil, true
	case versionsMsg:
		s.versions = m
		s.versionsTable.SetRows(versionRows(m))
		return s, nil, true
	case vulnMsg:
		s.spinner.Stop()
		if m.err != nil {
			s.err = m.err
			return s, nil, true
		}
		s.vulnCounts = m.counts
		s.viewState = ViewVulnerabilities
		return s, nil, true
	case resourceActionResultMsg:
		if m.err != nil {
			core.RecordJob(core.Job{
				ProjectID: s.projectID, Service: s.ShortName(),
				Resource: m.resource, Name: m.name, Action: m.action,
				Status: core.JobFailed, Error: m.err.Error(),
			})
			return s, func() tea.Msg {
				return core.ToastMsg{Message: m.err.Error(), Type: core.ToastError}
			}, true
		}
		core.RecordJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: m.resource, Name: m.name, Action: m.action,
			Status: core.JobSuccess,
		})
		var refresh tea.Cmd
		switch s.viewState {
		case ViewPackages:
			if s.selectedItem != nil {
				refresh = s.fetchPackagesCmd(*s.selectedItem)
			}
		case ViewVersions:
			if s.selectedPackage != nil {
				refresh = s.fetchVersionsCmd(*s.selectedPackage)
			}
		}
		return s, tea.Batch(func() tea.Msg {
			return core.ToastMsg{Message: m.msg, Type: core.ToastSuccess}
		}, refresh), true
	}
	return s, nil, false
}

// handleResourceKeyMsg processes keybindings for the Packages/Versions/
// Vulnerabilities/TagRemove sub-views. Returns (model, cmd, handled).
func (s *Service) handleResourceKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	var cmd tea.Cmd

	switch s.viewState {
	case ViewTagRemove:
		result, fcmd := s.tagRemoveForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewImages
			return s, nil, true
		}
		if result.Submitted && s.selectedItem != nil && s.selectedImage != nil {
			s.pendingTag = s.tagRemoveForm.Value("Tag")
			s.pendingAction = "untag"
			s.actionSource = ViewImages
			s.viewState = ViewConfirmation
			return s, nil, true
		}
		return s, fcmd, true

	case ViewVulnerabilities:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewImages
			return s, nil, true
		}
		return s, nil, true

	case ViewPackages:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewDetail
			return s, nil, true
		case "enter":
			if idx := s.packagesTable.Cursor(); idx >= 0 && idx < len(s.packages) {
				s.selectedPackage = &s.packages[idx]
				s.viewState = ViewVersions
				return s, s.fetchVersionsCmd(*s.selectedPackage), true
			}
			return s, nil, true
		case "d":
			if idx := s.packagesTable.Cursor(); idx >= 0 && idx < len(s.packages) {
				s.selectedPackage = &s.packages[idx]
				s.pendingAction = "delete-package"
				s.actionSource = ViewPackages
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.packagesTable.Update(msg)
		s.packagesTable = t
		return s, cmd, true

	case ViewVersions:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewPackages
			s.selectedPackage = nil
			return s, nil, true
		case "d":
			if idx := s.versionsTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.pendingAction = "delete-version"
				s.actionSource = ViewVersions
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.versionsTable.Update(msg)
		s.versionsTable = t
		return s, cmd, true
	}

	return s, nil, false
}

// -----------------------------------------------------------------------------
// Rendering
// -----------------------------------------------------------------------------

func (s *Service) renderResourceView() string {
	switch s.viewState {
	case ViewTagRemove:
		return s.tagRemoveForm.View()

	case ViewVulnerabilities:
		title := "Vulnerability Summary"
		if s.selectedImage != nil {
			title = "Vulnerability Summary: " + s.selectedImage.URI
		}
		if len(s.vulnCounts) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, title, "", "No vulnerability occurrences found (image may be unscanned).", "", styles.HelpStyle.Render("q Back"))
		}
		rows := make([]string, 0, len(s.vulnCounts))
		for _, c := range s.vulnCounts {
			rows = append(rows, fmt.Sprintf("%-20s fixable: %-6d total: %d", c.Severity, c.FixableCount, c.TotalCount))
		}
		return lipgloss.JoinVertical(lipgloss.Left, append([]string{title, ""}, append(rows, "", styles.HelpStyle.Render("q Back"))...)...)

	case ViewPackages:
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Repositories")
		if s.selectedItem != nil {
			breadcrumb = components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Repositories", s.selectedItem.Name, "Packages")
		}
		hint := styles.HelpStyle.Render("enter Versions  |  d Delete  |  q Back")
		if len(s.packages) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("packages"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.packagesTable.View(), "", hint)

	case ViewVersions:
		name := ""
		if s.selectedPackage != nil {
			name = s.selectedPackage.DisplayName
		}
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Packages", name, "Versions")
		hint := styles.HelpStyle.Render("d Delete  |  q Back")
		if len(s.versions) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("versions"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.versionsTable.View(), "", hint)
	}
	return ""
}

// renderResourceConfirmation renders the confirmation dialog for a pending
// resource-sub-view action. Returns (view, handled).
func (s *Service) renderResourceConfirmation() (string, bool) {
	switch s.pendingAction {
	case "untag":
		if s.selectedImage == nil {
			return "Error: No image selected", true
		}
		message := fmt.Sprintf("Remove tag %q from %s?", s.pendingTag, s.selectedImage.URI)
		return components.RenderConfirmationWithMessage("untag", s.pendingTag, "tag", message), true
	case "delete-package":
		if s.selectedPackage == nil {
			return "Error: No package selected", true
		}
		return components.RenderConfirmation("delete", s.selectedPackage.DisplayName, "package"), true
	case "delete-version":
		if s.selectedVersion == nil {
			return "Error: No version selected", true
		}
		return components.RenderConfirmation("delete", s.selectedVersion.Name, "version"), true
	}
	return "", false
}

// runResourceConfirmedAction executes the pending resource-sub-view action
// after a "y" confirmation. Returns (cmd, handled).
func (s *Service) runResourceConfirmedAction() (tea.Cmd, bool) {
	switch s.pendingAction {
	case "untag":
		if s.selectedItem != nil && s.selectedImage != nil {
			cmd := s.deleteTagCmd(s.repoFullName(*s.selectedItem), *s.selectedImage, s.pendingTag)
			s.viewState = ViewImages
			s.pendingTag = ""
			return cmd, true
		}
	case "delete-package":
		if s.selectedPackage != nil {
			cmd := s.deletePackageCmd(*s.selectedPackage)
			s.viewState = ViewPackages
			s.selectedPackage = nil
			return cmd, true
		}
	case "delete-version":
		if s.selectedVersion != nil {
			cmd := s.deleteVersionCmd(*s.selectedVersion)
			s.viewState = ViewVersions
			s.selectedVersion = nil
			return cmd, true
		}
	}
	return nil, false
}
