package cloudbuild

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// =============================================================================
// Models
// =============================================================================

// TriggerItem represents a Cloud Build trigger.
type TriggerItem struct {
	ID          string
	Name        string
	Description string
	RepoName    string
	BranchName  string
	Disabled    bool
	CreateTime  time.Time
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
	err error
	msg string
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
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateBuildTrigger(s.projectID, opts); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Created trigger %s", opts.Name)}
	}
}

func (s *Service) runTriggerCmd(t TriggerItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RunBuildTrigger(s.projectID, t.ID, t.BranchName); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Running trigger %s...", t.Name)}
	}
}

func (s *Service) deleteTriggerCmd(t TriggerItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteBuildTrigger(s.projectID, t.ID); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted trigger %s", t.Name)}
	}
}

func (s *Service) createWorkerPoolCmd(poolID, region string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateWorkerPool(s.projectID, region, poolID); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Creating worker pool %s...", poolID)}
	}
}

func (s *Service) deleteWorkerPoolCmd(wp WorkerPoolItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		region, id := parseRegionalName(wp.Name)
		if err := s.client.DeleteWorkerPool(s.projectID, region, id); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleting worker pool %s", id)}
	}
}

func (s *Service) deleteConnectionCmd(conn ConnectionItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		region, id := parseRegionalName(conn.Name)
		if err := s.client.DeleteConnection(s.projectID, region, id); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted connection %s", id)}
	}
}

func (s *Service) deleteCBRepositoryCmd(repo CBRepositoryItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteCBRepository(repo.Name); err != nil {
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleted repository %s", repo.Name)}
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
			s.err = m.err
			return s, nil, true
		}
		// Re-fetch whichever sub-view is active so the list reflects the change.
		switch s.viewState {
		case ViewTriggers:
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
		hint := styles.HelpStyle.Render("n New  |  R Run  |  d Delete  |  q Back")
		if len(s.triggers) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("triggers"), "", hint)
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.triggersTable.View(), "", hint)
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
