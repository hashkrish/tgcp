package cloudbuild

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// =============================================================================
// Models
// =============================================================================

// TriggerItem represents a Cloud Build trigger.
type TriggerItem struct {
	ID              string
	Name            string
	Description     string
	RepoName        string
	BranchName      string
	BuildConfigPath string
	Tags            []string
	Substitutions   map[string]string
	Disabled        bool
	CreateTime      time.Time
}

// WorkerPoolItem represents a private Cloud Build worker pool.
type WorkerPoolItem struct {
	Name        string // full resource name, used for delete
	DisplayName string
	State       string
	CreateTime  time.Time
}

// ConnectionItem represents a 2nd-gen source repository connection.
type ConnectionItem struct {
	Name       string // full resource name, used for delete/IAM/listing repos
	Provider   string
	Disabled   bool
	CreateTime time.Time
}

// CBRepositoryItem represents a repository linked to a 2nd-gen connection.
type CBRepositoryItem struct {
	Name       string // full resource name, used for delete
	RemoteURI  string
	CreateTime time.Time
}

// Message types for the resource sub-views' async loads/actions.
type triggersMsg []TriggerItem
type workerPoolsMsg []WorkerPoolItem
type connectionsMsg []ConnectionItem
type cbRepositoriesMsg []CBRepositoryItem
type resourceActionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// newTriggerCreateForm builds the FormModel for creating a repo-based build trigger.
func newTriggerCreateForm() components.FormModel {
	return components.NewForm("Create Build Trigger", []components.FormField{
		{Label: "Name", Placeholder: "my-trigger", Required: true},
		{Label: "Repo Name", Placeholder: "my-csr-repo", Required: true},
		{Label: "Branch Pattern", Default: "^main$", Required: true},
		{Label: "Build Config Path", Default: "cloudbuild.yaml", Required: true},
	})
}

// newTriggerEditForm builds the FormModel for editing an existing trigger's
// description, branch pattern, and build config path -- the same fields the
// Create form takes, plus description, matching this repo's single/few-field
// patch Update convention rather than a full trigger reconstruction.
func newTriggerEditForm(t TriggerItem) components.FormModel {
	return components.NewForm("Edit Trigger: "+t.Name, []components.FormField{
		{Label: "Description", Default: t.Description},
		{Label: "Branch Pattern", Default: t.BranchName, Required: true},
		{Label: "Build Config Path", Default: t.BuildConfigPath, Required: true},
	})
}

// newWorkerPoolCreateForm builds the FormModel for creating a private worker pool.
func newWorkerPoolCreateForm() components.FormModel {
	return components.NewForm("Create Worker Pool", []components.FormField{
		{Label: "Pool ID", Placeholder: "my-pool", Required: true},
		{Label: "Region", Placeholder: "us-central1", Required: true},
	})
}

// -----------------------------------------------------------------------------
// Fetch commands
// -----------------------------------------------------------------------------

func (s *Service) fetchTriggersCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListBuildTriggers(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		return triggersMsg(items)
	}
}

func (s *Service) fetchWorkerPoolsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListWorkerPools(s.projectID, s.wpRegion)
		if err != nil {
			return errMsg(err)
		}
		return workerPoolsMsg(items)
	}
}

func (s *Service) fetchConnectionsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListConnections(s.projectID, s.connRegion)
		if err != nil {
			return errMsg(err)
		}
		return connectionsMsg(items)
	}
}

func (s *Service) fetchCBRepositoriesCmd(connFullName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListCBRepositories(connFullName)
		if err != nil {
			return errMsg(err)
		}
		return cbRepositoriesMsg(items)
	}
}

// -----------------------------------------------------------------------------
// Action commands
// -----------------------------------------------------------------------------

func (s *Service) createTriggerCmd(opts TriggerCreateOpts) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "trigger", Name: opts.Name, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateBuildTrigger(s.projectID, opts)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "trigger", name: opts.Name, action: "create"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Created trigger %s", opts.Name), resource: "trigger", name: opts.Name, action: "create"}
	}
}

func (s *Service) runTriggerCmd(t TriggerItem) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "trigger", Name: t.Name, Action: "run",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.RunBuildTrigger(s.projectID, t.ID, t.RepoName, t.BranchName)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "trigger", name: t.Name, action: "run"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Running trigger %s...", t.Name), resource: "trigger", name: t.Name, action: "run"}
	}
}

func (s *Service) updateTriggerCmd(t TriggerItem, opts TriggerUpdateOpts) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "trigger", Name: t.Name, Action: "update",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.UpdateBuildTrigger(s.projectID, t.ID, opts)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "trigger", name: t.Name, action: "update"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Updated trigger %s", t.Name), resource: "trigger", name: t.Name, action: "update"}
	}
}

func (s *Service) deleteTriggerCmd(t TriggerItem) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "trigger", Name: t.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteBuildTrigger(s.projectID, t.ID)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "trigger", name: t.Name, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted trigger %s", t.Name), resource: "trigger", name: t.Name, action: "delete"}
	}
}

func (s *Service) createWorkerPoolCmd(poolID, region string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "worker pool", Name: poolID, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateWorkerPool(s.projectID, region, poolID)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "worker pool", name: poolID, action: "create"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Creating worker pool %s...", poolID), resource: "worker pool", name: poolID, action: "create"}
	}
}

func (s *Service) deleteWorkerPoolCmd(wp WorkerPoolItem) tea.Cmd {
	return func() tea.Msg {
		region, id := parseRegionalName(wp.Name)
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "worker pool", Name: id, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteWorkerPool(s.projectID, region, id)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "worker pool", name: id, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleting worker pool %s", id), resource: "worker pool", name: id, action: "delete"}
	}
}

func (s *Service) deleteConnectionCmd(conn ConnectionItem) tea.Cmd {
	return func() tea.Msg {
		region, id := parseRegionalName(conn.Name)
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "connection", Name: id, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteConnection(s.projectID, region, id)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "connection", name: id, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted connection %s", id), resource: "connection", name: id, action: "delete"}
	}
}

func (s *Service) deleteCBRepositoryCmd(repo CBRepositoryItem) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "repository", Name: repo.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteCBRepository(repo.Name)
		})
		if err != nil {
			return resourceActionResultMsg{err: err, resource: "repository", name: repo.Name, action: "delete"}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted repository %s", repo.Name), resource: "repository", name: repo.Name, action: "delete"}
	}
}

// parseRegionalName extracts the region and final ID segment from a
// "projects/P/locations/REGION/.../ID" resource name.
func parseRegionalName(name string) (region, id string) {
	parts := splitPath(name)
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "locations" {
			region = parts[i+1]
		}
	}
	if len(parts) > 0 {
		id = parts[len(parts)-1]
	}
	return region, id
}

func splitPath(s string) []string {
	var parts []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

// -----------------------------------------------------------------------------
// Table builders
// -----------------------------------------------------------------------------

func newTriggersTable() *components.StandardTable {
	return components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 24},
		{Title: "Repo", Width: 20},
		{Title: "Branch", Width: 16},
		{Title: "Disabled", Width: 10},
	})
}

func triggerRows(items []TriggerItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, t := range items {
		rows = append(rows, table.Row{t.Name, t.RepoName, t.BranchName, fmt.Sprintf("%t", t.Disabled)})
	}
	return rows
}

func newWorkerPoolsTable() *components.StandardTable {
	return components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 30},
		{Title: "State", Width: 14},
		{Title: "Created", Width: 19},
	})
}

func workerPoolRows(items []WorkerPoolItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, wp := range items {
		created := ""
		if !wp.CreateTime.IsZero() {
			created = wp.CreateTime.Format("2006-01-02 15:04")
		}
		rows = append(rows, table.Row{wp.DisplayName, wp.State, created})
	}
	return rows
}

func newConnectionsTable() *components.StandardTable {
	return components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 30},
		{Title: "Provider", Width: 20},
		{Title: "Disabled", Width: 10},
	})
}

func connectionRows(items []ConnectionItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, c := range items {
		_, id := parseRegionalName(c.Name)
		rows = append(rows, table.Row{id, c.Provider, fmt.Sprintf("%t", c.Disabled)})
	}
	return rows
}

func newCBRepositoriesTable() *components.StandardTable {
	return components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 24},
		{Title: "Remote URI", Width: 50},
	})
}

func cbRepositoryRows(items []CBRepositoryItem) []table.Row {
	rows := make([]table.Row, 0, len(items))
	for _, r := range items {
		_, id := parseRegionalName(r.Name)
		rows = append(rows, table.Row{id, r.RemoteURI})
	}
	return rows
}

// -----------------------------------------------------------------------------
// Update handling (dispatched from Service.Update for the resource sub-views)
// -----------------------------------------------------------------------------

// handleResourceMsg processes the async load/action messages for the
// Triggers/WorkerPools/Connections/Repositories sub-views. Returns
// (model, cmd, handled) -- handled is false if msg wasn't one of these types.
func (s *Service) handleResourceMsg(msg tea.Msg) (tea.Model, tea.Cmd, bool) {
	switch m := msg.(type) {
	case triggersMsg:
		s.triggers = m
		s.triggersTable.SetRows(triggerRows(m))
		// Repoint selectedTrigger at its refreshed copy (by ID) so a detail/edit
		// view in progress reflects the latest data instead of going stale.
		if s.selectedTrigger != nil {
			for i := range s.triggers {
				if s.triggers[i].ID == s.selectedTrigger.ID {
					s.selectedTrigger = &s.triggers[i]
					break
				}
			}
		}
		return s, nil, true
	case workerPoolsMsg:
		s.workerPools = m
		s.workerPoolsTable.SetRows(workerPoolRows(m))
		return s, nil, true
	case connectionsMsg:
		s.connections = m
		s.connectionsTable.SetRows(connectionRows(m))
		return s, nil, true
	case cbRepositoriesMsg:
		s.cbRepositories = m
		s.cbRepositoriesTable.SetRows(cbRepositoryRows(m))
		return s, nil, true
	case resourceActionResultMsg:
		if m.err != nil {
			// Don't set s.err here: that field drives the full-screen "Error
			// Loading Builds" overlay in View(), and an action failure (e.g.
			// running/updating a trigger) isn't a list-load failure. The
			// caller in Update() already surfaces m.err via a toast.
			return s, nil, true
		}
		// Re-fetch whichever sub-view is active so the list reflects the change.
		switch s.viewState {
		case ViewTriggers, ViewTriggerDetail, ViewTriggerEdit:
			return s, s.fetchTriggersCmd(), true
		case ViewWorkerPools:
			return s, s.fetchWorkerPoolsCmd(), true
		case ViewConnections:
			return s, s.fetchConnectionsCmd(), true
		case ViewCBRepositories:
			if s.selectedConnection != nil {
				return s, s.fetchCBRepositoriesCmd(s.selectedConnection.Name), true
			}
		}
		return s, nil, true
	}
	return s, nil, false
}

// handleResourceKeyMsg processes keybindings for the resource sub-views.
// Returns (model, cmd, handled).
func (s *Service) handleResourceKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	var cmd tea.Cmd

	switch s.viewState {
	case ViewTriggers:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewList
			return s, nil, true
		case "n":
			s.triggerForm = newTriggerCreateForm()
			s.viewState = ViewTriggerCreate
			return s, nil, true
		case "enter":
			if idx := s.triggersTable.Cursor(); idx >= 0 && idx < len(s.triggers) {
				s.selectedTrigger = &s.triggers[idx]
				s.viewState = ViewTriggerDetail
			}
			return s, nil, true
		case "R": // Run
			if idx := s.triggersTable.Cursor(); idx >= 0 && idx < len(s.triggers) {
				s.selectedTrigger = &s.triggers[idx]
				s.pendingAction = "run-trigger"
				s.actionSource = ViewTriggers
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		case "d":
			if idx := s.triggersTable.Cursor(); idx >= 0 && idx < len(s.triggers) {
				s.selectedTrigger = &s.triggers[idx]
				s.pendingAction = "delete-trigger"
				s.actionSource = ViewTriggers
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.triggersTable.Update(msg)
		s.triggersTable = t
		return s, cmd, true

	case ViewTriggerDetail:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewTriggers
			s.selectedTrigger = nil
			return s, nil, true
		case "e":
			if s.selectedTrigger != nil {
				s.triggerForm = newTriggerEditForm(*s.selectedTrigger)
				s.viewState = ViewTriggerEdit
			}
			return s, nil, true
		case "E": // Enable/Disable toggle
			if s.selectedTrigger != nil {
				if s.selectedTrigger.Disabled {
					s.pendingAction = "enable-trigger"
				} else {
					s.pendingAction = "disable-trigger"
				}
				s.actionSource = ViewTriggerDetail
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		case "R": // Run
			if s.selectedTrigger != nil {
				s.pendingAction = "run-trigger"
				s.actionSource = ViewTriggerDetail
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		case "d":
			if s.selectedTrigger != nil {
				s.pendingAction = "delete-trigger"
				s.actionSource = ViewTriggerDetail
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		return s, nil, true

	case ViewTriggerEdit:
		result, fcmd := s.triggerForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewTriggerDetail
			return s, nil, true
		}
		if result.Submitted && s.selectedTrigger != nil {
			vals := s.triggerForm.Values()
			t := *s.selectedTrigger
			s.viewState = ViewTriggerDetail
			return s, s.updateTriggerCmd(t, TriggerUpdateOpts{
				Description:     vals["Description"],
				BranchPattern:   vals["Branch Pattern"],
				BuildConfigPath: vals["Build Config Path"],
			}), true
		}
		return s, fcmd, true

	case ViewTriggerCreate:
		result, fcmd := s.triggerForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewTriggers
			return s, nil, true
		}
		if result.Submitted {
			vals := s.triggerForm.Values()
			s.viewState = ViewTriggers
			return s, s.createTriggerCmd(TriggerCreateOpts{
				Name:            vals["Name"],
				RepoName:        vals["Repo Name"],
				BranchPattern:   vals["Branch Pattern"],
				BuildConfigPath: vals["Build Config Path"],
			}), true
		}
		return s, fcmd, true

	case ViewWorkerPools:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewList
			return s, nil, true
		case "n":
			s.workerPoolForm = newWorkerPoolCreateForm()
			s.viewState = ViewWorkerPoolCreate
			return s, nil, true
		case "d":
			if idx := s.workerPoolsTable.Cursor(); idx >= 0 && idx < len(s.workerPools) {
				s.selectedWorkerPool = &s.workerPools[idx]
				s.pendingAction = "delete-worker-pool"
				s.actionSource = ViewWorkerPools
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.workerPoolsTable.Update(msg)
		s.workerPoolsTable = t
		return s, cmd, true

	case ViewWorkerPoolCreate:
		result, fcmd := s.workerPoolForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewWorkerPools
			return s, nil, true
		}
		if result.Submitted {
			vals := s.workerPoolForm.Values()
			s.wpRegion = vals["Region"]
			s.viewState = ViewWorkerPools
			return s, s.createWorkerPoolCmd(vals["Pool ID"], vals["Region"]), true
		}
		return s, fcmd, true

	case ViewConnections:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewList
			return s, nil, true
		case "enter":
			if idx := s.connectionsTable.Cursor(); idx >= 0 && idx < len(s.connections) {
				s.selectedConnection = &s.connections[idx]
				s.viewState = ViewCBRepositories
				return s, s.fetchCBRepositoriesCmd(s.selectedConnection.Name), true
			}
			return s, nil, true
		case "d":
			if idx := s.connectionsTable.Cursor(); idx >= 0 && idx < len(s.connections) {
				s.selectedConnection = &s.connections[idx]
				s.pendingAction = "delete-connection"
				s.actionSource = ViewConnections
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.connectionsTable.Update(msg)
		s.connectionsTable = t
		return s, cmd, true

	case ViewCBRepositories:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewConnections
			s.selectedConnection = nil
			return s, nil, true
		case "d":
			if idx := s.cbRepositoriesTable.Cursor(); idx >= 0 && idx < len(s.cbRepositories) {
				s.selectedCBRepository = &s.cbRepositories[idx]
				s.pendingAction = "delete-repository"
				s.actionSource = ViewCBRepositories
				s.viewState = ViewConfirmation
			}
			return s, nil, true
		}
		var t *components.StandardTable
		t, cmd = s.cbRepositoriesTable.Update(msg)
		s.cbRepositoriesTable = t
		return s, cmd, true
	}

	return s, nil, false
}

// -----------------------------------------------------------------------------
// Rendering
// -----------------------------------------------------------------------------

func (s *Service) renderResourceView() string {
	switch s.viewState {
	case ViewTriggers:
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Triggers")
		hint := styles.HelpStyle.Render("n New  |  Enter Detail  |  R Run  |  d Delete  |  q Back")
		if len(s.triggers) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("triggers"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.triggersTable.View(), "", hint)
	case ViewTriggerDetail:
		return s.renderTriggerDetailView()
	case ViewTriggerEdit:
		return s.triggerForm.View()
	case ViewTriggerCreate:
		return s.triggerForm.View()
	case ViewWorkerPools:
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Worker Pools")
		hint := styles.HelpStyle.Render("n New  |  d Delete  |  q Back")
		if len(s.workerPools) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("worker pools"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.workerPoolsTable.View(), "", hint)
	case ViewWorkerPoolCreate:
		return s.workerPoolForm.View()
	case ViewConnections:
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Connections")
		hint := styles.HelpStyle.Render("enter Repositories  |  d Delete  |  q Back  (create via Console/gcloud -- needs an interactive App-install OAuth flow)")
		if len(s.connections) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("connections"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.connectionsTable.View(), "", hint)
	case ViewCBRepositories:
		name := ""
		if s.selectedConnection != nil {
			_, name = parseRegionalName(s.selectedConnection.Name)
		}
		breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Connections", name, "Repositories")
		hint := styles.HelpStyle.Render("d Delete  |  q Back")
		if len(s.cbRepositories) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("repositories"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.cbRepositoriesTable.View(), "", hint)
	}
	return ""
}

// renderTriggerDetailView renders the detail card for the selected trigger.
func (s *Service) renderTriggerDetailView() string {
	if s.selectedTrigger == nil {
		return "Error: No trigger selected"
	}
	t := s.selectedTrigger

	breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Triggers", t.Name)

	rows := []components.KeyValue{
		{Key: "ID", Value: t.ID},
		{Key: "Name", Value: t.Name},
	}
	if t.Description != "" {
		rows = append(rows, components.KeyValue{Key: "Description", Value: t.Description})
	}
	rows = append(rows,
		components.KeyValue{Key: "Repo", Value: t.RepoName},
		components.KeyValue{Key: "Branch Pattern", Value: t.BranchName},
		components.KeyValue{Key: "Build Config Path", Value: t.BuildConfigPath},
	)
	if len(t.Tags) > 0 {
		rows = append(rows, components.KeyValue{Key: "Tags", Value: strings.Join(t.Tags, ", ")})
	}
	if len(t.Substitutions) > 0 {
		rows = append(rows, components.KeyValue{Key: "Substitutions", Value: formatSubstitutions(t.Substitutions)})
	}
	rows = append(rows, components.KeyValue{Key: "Disabled", Value: fmt.Sprintf("%t", t.Disabled)})
	if !t.CreateTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Created", Value: t.CreateTime.Format("2006-01-02 15:04:05")})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Trigger Details",
		Rows:  rows,
	})

	hint := styles.HelpStyle.Render("e Edit  |  E Enable/Disable  |  R Run  |  d Delete  |  q Back")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		hint,
	)
}

// renderResourceConfirmation renders the confirmation dialog for a pending
// resource-sub-view action. Returns "" if pendingAction isn't one of these.
func (s *Service) renderResourceConfirmation() (string, bool) {
	switch s.pendingAction {
	case "run-trigger":
		if s.selectedTrigger == nil {
			return "Error: No trigger selected", true
		}
		return components.RenderConfirmation("run", s.selectedTrigger.Name, "trigger"), true
	case "delete-trigger":
		if s.selectedTrigger == nil {
			return "Error: No trigger selected", true
		}
		return components.RenderConfirmation("delete", s.selectedTrigger.Name, "trigger"), true
	case "enable-trigger":
		if s.selectedTrigger == nil {
			return "Error: No trigger selected", true
		}
		return components.RenderConfirmation("enable", s.selectedTrigger.Name, "trigger"), true
	case "disable-trigger":
		if s.selectedTrigger == nil {
			return "Error: No trigger selected", true
		}
		return components.RenderConfirmation("disable", s.selectedTrigger.Name, "trigger"), true
	case "delete-worker-pool":
		if s.selectedWorkerPool == nil {
			return "Error: No worker pool selected", true
		}
		return components.RenderConfirmation("delete", s.selectedWorkerPool.DisplayName, "worker pool"), true
	case "delete-connection":
		if s.selectedConnection == nil {
			return "Error: No connection selected", true
		}
		_, id := parseRegionalName(s.selectedConnection.Name)
		return components.RenderConfirmation("delete", id, "connection"), true
	case "delete-repository":
		if s.selectedCBRepository == nil {
			return "Error: No repository selected", true
		}
		_, id := parseRegionalName(s.selectedCBRepository.Name)
		return components.RenderConfirmation("delete", id, "repository"), true
	}
	return "", false
}

// runResourceConfirmedAction executes the pending resource-sub-view action
// after a "y" confirmation. Returns (cmd, handled).
func (s *Service) runResourceConfirmedAction() (tea.Cmd, bool) {
	switch s.pendingAction {
	case "run-trigger":
		if s.selectedTrigger != nil {
			cmd := s.runTriggerCmd(*s.selectedTrigger)
			s.viewState = ViewTriggers
			return cmd, true
		}
	case "delete-trigger":
		if s.selectedTrigger != nil {
			cmd := s.deleteTriggerCmd(*s.selectedTrigger)
			s.viewState = ViewTriggers
			s.selectedTrigger = nil
			return cmd, true
		}
	case "enable-trigger", "disable-trigger":
		if s.selectedTrigger != nil {
			disabled := s.pendingAction == "disable-trigger"
			cmd := s.updateTriggerCmd(*s.selectedTrigger, TriggerUpdateOpts{Disabled: &disabled})
			s.viewState = ViewTriggerDetail
			return cmd, true
		}
	case "delete-worker-pool":
		if s.selectedWorkerPool != nil {
			cmd := s.deleteWorkerPoolCmd(*s.selectedWorkerPool)
			s.viewState = ViewWorkerPools
			s.selectedWorkerPool = nil
			return cmd, true
		}
	case "delete-connection":
		if s.selectedConnection != nil {
			cmd := s.deleteConnectionCmd(*s.selectedConnection)
			s.viewState = ViewConnections
			s.selectedConnection = nil
			return cmd, true
		}
	case "delete-repository":
		if s.selectedCBRepository != nil {
			cmd := s.deleteCBRepositoryCmd(*s.selectedCBRepository)
			s.viewState = ViewCBRepositories
			s.selectedCBRepository = nil
			return cmd, true
		}
	}
	return nil, false
}
