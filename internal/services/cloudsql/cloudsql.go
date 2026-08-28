package cloudsql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines whether we are listing or viewing details
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewConfirmation
	ViewCreate
	ViewUpdate
	ViewQuery       // Entering a read-only SQL statement to run (Data-plane)
	ViewQueryResult // Showing the result of a submitted query
)

// newInstanceUpdateForm builds the FormModel for updating a Cloud SQL
// instance's machine tier, seeded with its current value. This is the
// simplest single-field Update flow (`gcloud sql instances patch --tier`) —
// full `patch` surface coverage (storage, flags, backups, etc.) is out of
// scope.
func newInstanceUpdateForm(inst Instance) components.FormModel {
	return components.NewForm("Update Cloud SQL Instance: "+inst.Name, []components.FormField{
		{Label: "Tier", Default: inst.Tier, Placeholder: "db-custom-2-8192", Required: true},
	})
}

// newQueryForm builds the FormModel for the read-only "Execute SQL"
// data-plane feature: a database name and a single SQL statement. The
// statement is re-validated with ValidateReadOnlySQL on submit so only
// SELECT/WITH/SHOW/EXPLAIN/DESCRIBE statements can ever be sent.
func newQueryForm(inst Instance) components.FormModel {
	return components.NewForm("Execute SQL (read-only): "+inst.Name, []components.FormField{
		{Label: "Database", Placeholder: "postgres", Required: true},
		{
			Label:       "SQL Statement",
			Placeholder: "SELECT * FROM my_table LIMIT 10",
			Required:    true,
			Validate: func(value string) string {
				if err := ValidateReadOnlySQL(value); err != nil {
					return err.Error()
				}
				return ""
			},
		},
	})
}

// newInstanceCreateForm builds the FormModel for creating a new Cloud SQL instance.
func newInstanceCreateForm() components.FormModel {
	return components.NewForm("Create Cloud SQL Instance", []components.FormField{
		{Label: "Name", Placeholder: "my-instance", Required: true},
		{Label: "Region", Placeholder: "us-central1", Required: true},
		{Label: "Database Version", Default: "POSTGRES_15", Required: true},
		{Label: "Tier", Default: "db-f1-micro", Required: true},
	})
}

// Service implements the generic Service interface
type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	// UI Components
	filter        components.FilterModel
	filterSession components.FilterSession[Instance]
	spinner       components.SpinnerModel

	// State
	instances []Instance
	err       error

	// View State
	viewState        ViewState
	selectedInstance *Instance

	// Confirmation State
	pendingAction string    // "start" or "stop"
	actionSource  ViewState // Where to return after confirmation

	// Create State
	createForm components.FormModel

	// Update State
	updateForm components.FormModel

	// Query State (read-only "Execute SQL" data-plane feature)
	queryForm   components.FormModel
	queryResult *QueryResult
	queryErr    error
	queryTable  *components.StandardTable

	// Cache
	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Table Setup
	columns := []table.Column{
		{Title: "Name", Width: 50},
		{Title: "Status", Width: 15},
		{Title: "Version", Width: 20},
		{Title: "Region", Width: 15},
		{Title: "Primary IP", Width: 20},
		{Title: "Tier", Width: 20},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredInstances, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud SQL"
}

func (s *Service) ShortName() string {
	return "sql"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  s:Start  x:Stop  t:Restart  l:Logs  Ent:Detail  c:Create  u:Update  e:Execute SQL  d:Delete"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  s:Start  x:Stop  t:Restart  u:Update  e:Execute SQL  d:Delete"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewQuery {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	}
	if s.viewState == ViewQueryResult {
		return "Esc/q:Back"
	}
	return ""
}

func (s *Service) Focus() {
	s.table.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
}

func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx)
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchInstancesCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Msg types
type instancesMsg []Instance
type errMsg error
type actionResultMsg struct {
	err error
	msg string
}
type queryResultMsg struct {
	result *QueryResult
	err    error
}

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchInstancesCmd(false), s.tick())

	case instancesMsg:
		s.spinner.Stop()
		s.instances = msg
		s.filterSession.Apply(s.instances)
		if s.selectedInstance != nil {
			for i := range s.instances {
				if s.instances[i].Name == s.selectedInstance.Name {
					s.selectedInstance = &s.instances[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case queryResultMsg:
		s.spinner.Stop()
		s.queryResult = msg.result
		s.queryErr = msg.err
		s.viewState = ViewQueryResult
		if msg.result != nil {
			s.updateQueryTable(msg.result)
		}
		return s, nil

	case actionResultMsg:
		if msg.err != nil {
			s.err = msg.err
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		} else if msg.msg != "" {
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		return s, s.Refresh()

	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		// Handle filter mode (only in list view)
		if s.viewState == ViewList {
			result := s.filterSession.HandleKey(msg)

			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
				// Continue processing other keys
			}
		}

		switch s.viewState {
		case ViewList:
			switch msg.String() {
			case "r":
				return s, s.fetchInstancesCmd(true)
			case "enter":
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.viewState = ViewDetail
				}
			case "s": // Start
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "start"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "x": // Stop
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "stop"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "t": // Restart
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "restart"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "l": // Logs
				if idx := s.table.Cursor(); idx >= 0 && idx < len(s.instances) {
					inst := s.instances[idx]
					// Cloud SQL filter uses database_id usually project:instance
					filter := fmt.Sprintf(`resource.type="cloudsql_database" AND resource.labels.database_id="%s:%s"`, s.projectID, inst.Name)
					heading := fmt.Sprintf("Database: %s", inst.Name)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "sql", Heading: heading} }
				}
			case "c": // Create
				s.createForm = newInstanceCreateForm()
				s.viewState = ViewCreate
				return s, nil
			case "e": // Execute SQL (read-only, data-plane)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.queryForm = newQueryForm(*s.selectedInstance)
					s.viewState = ViewQuery
					return s, nil
				}
			case "u": // Update
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.updateForm = newInstanceUpdateForm(*s.selectedInstance)
					s.viewState = ViewUpdate
					return s, nil
				}
			case "d": // Delete (Confirm, double)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "delete"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
					return s, nil
				}
			}
			s.table, cmd = s.table.Update(msg)
			return s, cmd

		case ViewDetail:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedInstance = nil
				return s, nil
			case "s":
				if s.selectedInstance != nil {
					s.pendingAction = "start"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "x":
				if s.selectedInstance != nil {
					s.pendingAction = "stop"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "u":
				if s.selectedInstance != nil {
					s.updateForm = newInstanceUpdateForm(*s.selectedInstance)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "d": // Delete (Confirm, double)
				if s.selectedInstance != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "t":
				if s.selectedInstance != nil {
					s.pendingAction = "restart"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "e": // Execute SQL (read-only, data-plane)
				if s.selectedInstance != nil {
					s.queryForm = newQueryForm(*s.selectedInstance)
					s.viewState = ViewQuery
				}
				return s, nil
			}

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				if s.pendingAction == "delete" {
					// First confirmation only escalates to a second one —
					// deleting a Cloud SQL instance destroys all its
					// databases with no undo.
					s.pendingAction = "delete-confirm2"
					return s, nil
				}
				var actionCmd tea.Cmd
				switch s.pendingAction {
				case "start":
					actionCmd = s.startInstanceCmd(*s.selectedInstance)
				case "stop":
					actionCmd = s.stopInstanceCmd(*s.selectedInstance)
				case "delete-confirm2":
					if s.selectedInstance != nil {
						actionCmd = s.deleteInstanceCmd(*s.selectedInstance)
						s.selectedInstance = nil
					}
					s.actionSource = ViewList
				case "restart":
					actionCmd = s.restartInstanceCmd(*s.selectedInstance)
				}
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, actionCmd

			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}

		case ViewCreate:
			result, fcmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				vals := s.createForm.Values()
				s.viewState = ViewList
				return s, s.CreateInstanceCmd(vals["Name"], vals["Region"], vals["Database Version"], vals["Tier"])
			}
			return s, fcmd

		case ViewUpdate:
			result, fcmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				inst := *s.selectedInstance
				tier := s.updateForm.Value("Tier")
				s.viewState = ViewList
				return s, s.updateInstanceTierCmd(inst, tier)
			}
			return s, fcmd

		case ViewQuery:
			result, fcmd := s.queryForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				if s.selectedInstance == nil {
					s.viewState = ViewList
				}
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				inst := *s.selectedInstance
				database := s.queryForm.Value("Database")
				stmt := s.queryForm.Value("SQL Statement")
				return s, tea.Batch(s.spinner.Start("Running query..."), s.executeQueryCmd(inst, database, stmt))
			}
			return s, fcmd

		case ViewQueryResult:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewDetail
				if s.selectedInstance == nil {
					s.viewState = ViewList
				}
				s.queryResult = nil
				s.queryErr = nil
				return s, nil
			}
			if s.queryTable != nil {
				var updatedTable *components.StandardTable
				updatedTable, cmd = s.queryTable.Update(msg)
				s.queryTable = updatedTable
				return s, cmd
			}
		}
	}

	return s, nil
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Instances")
	}

	// Show animated spinner while loading
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

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewQuery {
		return s.queryForm.View()
	}

	if s.viewState == ViewQueryResult {
		return s.renderQueryResult()
	}

	// Default: List View
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")

	if len(s.instances) == 0 {
		content.WriteString(components.EmptyState("instances"))
		return content.String()
	}

	// Status Summary (Count pills)
	states := make([]string, 0, len(s.instances))
	for _, inst := range s.instances {
		states = append(states, string(inst.State))
	}
	content.WriteString(components.StatusSummary(states))
	content.WriteString("\n\n")

	content.WriteString(s.table.View())
	return content.String()
}

// Cmd to fetch instances
func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "sql_instances"

		// 1. Check Cache
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if insts, ok := val.([]Instance); ok {
					return instancesMsg(insts)
				}
			}
		}

		// 2. API Call
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		insts, err := s.client.ListInstances(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		// 3. Update Cache
		if s.cache != nil {
			s.cache.Set(key, insts, CacheTTL)
		}

		return instancesMsg(insts)
	}
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchInstancesCmd(false),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedInstance = nil
	s.err = nil // Fix: Clear previous errors on reset
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// Internal Helpers

func (s *Service) updateTable(instances []Instance) {
	rows := make([]table.Row, len(instances))
	for i, inst := range instances {
		state := string(inst.State)
		if state == "" {
			state = "UNKNOWN"
		}

		rows[i] = table.Row{
			inst.Name,
			state,
			inst.DatabaseVersion,
			inst.Region,
			inst.PrimaryIP,
			inst.Tier,
		}
	}
	s.table.SetRows(rows)
}

// getFilteredInstances returns filtered instances based on the query string
func (s *Service) getFilteredInstances(instances []Instance, query string) []Instance {
	if query == "" {
		return instances
	}
	return components.FilterSlice(instances, query, func(inst Instance, q string) bool {
		return components.ContainsMatch(inst.Name, string(inst.State), inst.DatabaseVersion, inst.Region, inst.PrimaryIP, inst.Tier)(q)
	})
}

func (s *Service) startInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		err := s.client.StartInstance(s.projectID, i.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Starting instance %s...", i.Name)}
	}
}

// CreateInstanceCmd triggers creation of a new Cloud SQL instance
func (s *Service) CreateInstanceCmd(name, region, dbVersion, tier string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateInstance(s.projectID, name, region, dbVersion, tier); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating instance %s...", name)}
	}
}

func (s *Service) stopInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		err := s.client.StopInstance(s.projectID, i.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopping instance %s...", i.Name)}
	}
}

// deleteInstanceCmd triggers deletion of an existing Cloud SQL instance
func (s *Service) deleteInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteInstance(s.projectID, i.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting instance %s...", i.Name)}
	}
}

// updateInstanceTierCmd triggers a tier-patch update for an existing Cloud SQL instance
func (s *Service) updateInstanceTierCmd(i Instance, tier string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateInstanceTier(s.projectID, i.Name, tier); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating tier for instance %s...", i.Name)}
	}
}

// executeQueryCmd runs a single read-only SQL statement against a Cloud SQL
// instance. The statement is validated (again) inside Client.ExecuteQuery,
// so this command can never issue a write.
func (s *Service) executeQueryCmd(i Instance, database, stmt string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return queryResultMsg{err: fmt.Errorf("client not initialized")}
		}
		result, err := s.client.ExecuteQuery(s.projectID, i.Name, database, stmt)
		if err != nil {
			return queryResultMsg{err: err}
		}
		return queryResultMsg{result: result}
	}
}

// updateQueryTable rebuilds s.queryTable's columns and rows from a query
// result. A fresh StandardTable is created per query since the column set
// varies with the statement.
func (s *Service) updateQueryTable(result *QueryResult) {
	columns := make([]table.Column, len(result.Columns))
	for i, c := range result.Columns {
		width := len(c.Name) + 4
		if width < 12 {
			width = 12
		}
		if width > 30 {
			width = 30
		}
		columns[i] = table.Column{Title: c.Name, Width: width}
	}

	rows := make([]table.Row, len(result.Rows))
	for i, r := range result.Rows {
		rows[i] = table.Row(r.Values)
	}

	t := components.NewStandardTable(columns)
	t.SetRows(rows)
	s.queryTable = t
}

func (s *Service) restartInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		err := s.client.RestartInstance(s.projectID, i.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Restarting instance %s...", i.Name)}
	}
}
