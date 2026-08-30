package spanner

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewIAMForm
	ViewQuery
	ViewQueryResult
)

// newQueryForm builds the FormModel for the read-only "Execute SQL"
// data-plane feature: a database ID and a single SQL statement. The
// statement is re-validated with validateReadOnlySQL on submit so only
// SELECT/WITH statements can ever be sent.
func newQueryForm(inst Instance) components.FormModel {
	return components.NewForm("Execute SQL (read-only): "+inst.Name, []components.FormField{
		{Label: "Database ID", Placeholder: "my-database", Required: true},
		{
			Label:       "SQL Statement",
			Placeholder: "SELECT * FROM MyTable LIMIT 10",
			Required:    true,
			Validate: func(value string) string {
				if err := validateReadOnlySQL(value); err != nil {
					return err.Error()
				}
				return ""
			},
		},
	})
}

// newInstanceIAMForm builds the FormModel for granting an IAM role on a Spanner instance.
func newInstanceIAMForm() components.FormModel {
	return components.NewForm("Add IAM Binding", []components.FormField{
		{Label: "Role", Placeholder: "roles/spanner.databaseUser", Required: true},
		{Label: "Member", Placeholder: "user:name@example.com", Required: true},
	})
}

// newInstanceUpdateForm builds the FormModel for updating a Spanner
// instance's node count, seeded with its current value. DDL update and
// instance `move`/`change-quorum` are out of scope.
func newInstanceUpdateForm(inst Instance) components.FormModel {
	return components.NewForm("Update Spanner Instance: "+inst.Name, []components.FormField{
		{Label: "Node Count", Default: strconv.Itoa(inst.NodeCount), Required: true, Validate: func(v string) string {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

type instancesMsg []Instance
type errMsg error

// queryResultMsg carries the result of an ExecuteQuery call.
type queryResultMsg struct {
	result *QueryResult
	err    error
}

// actionResultMsg carries the result of an async action (e.g. instance creation)
type actionResultMsg struct {
	err error
	msg string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Instance]

	instances []Instance
	spinner   components.SpinnerModel
	err       error

	viewState        ViewState
	selectedInstance *Instance

	createForm components.FormModel
	updateForm components.FormModel
	iamForm    components.FormModel

	// Query State (read-only "Execute SQL" data-plane feature)
	queryForm   components.FormModel
	queryResult *QueryResult
	queryErr    error
	queryTable  *components.StandardTable

	// pendingIAMRole/pendingIAMMember are captured at form-submit time so
	// the confirmation dialog and the actual API call use the same values
	// regardless of what the form fields hold later.
	pendingIAMRole   string
	pendingIAMMember string

	// Confirmation State
	pendingAction string    // "delete", "grant"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Nodes / PUs", Width: 15},
		{Title: "Config", Width: 20},
		{Title: "State", Width: 10},
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
	return "Spanner"
}

func (s *Service) ShortName() string {
	return "spanner"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Instance"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewIAMForm || s.viewState == ViewQuery {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  d:Delete  i:Add IAM Binding  e:Execute SQL"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewQueryResult {
		return "Esc/q:Back"
	}
	return "Esc/q:Back"
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

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

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchInstancesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedInstance = nil
	s.queryResult = nil
	s.queryErr = nil
	s.err = nil
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.table.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
}

// -----------------------------------------------------------------------------
// Update
// -----------------------------------------------------------------------------

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
		if msg.err == nil {
			s.updateQueryTable(msg.result)
		}
		s.viewState = ViewQueryResult
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			instName := ""
			if s.selectedInstance != nil {
				instName = s.selectedInstance.Name
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "instance",
				Name: instName, Action: "delete", Status: status, Error: errStr,
			})
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			s.selectedInstance = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			instName := ""
			if s.selectedInstance != nil {
				instName = s.selectedInstance.Name
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "instance",
				Name: instName, Action: "grant", Status: status, Error: errStr,
			})
			return s, func() tea.Msg {
				if msg.err != nil {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			}
		}
		{
			action := "create"
			instName := s.createForm.Value("Instance ID")
			if s.viewState == ViewUpdate {
				action = "update"
				if s.selectedInstance != nil {
					instName = s.selectedInstance.Name
				}
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "instance",
				Name: instName, Action: action, Status: status, Error: errStr,
			})
		}
		if msg.err != nil {
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
		s.viewState = ViewList
		return s, tea.Batch(
			func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			},
			s.Refresh(),
		)

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
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				return s, s.createInstanceCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.updateInstanceCmd(*s.selectedInstance)
			}
			return s, formCmd
		}

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

		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("Create Spanner Instance", []components.FormField{
					{Label: "Instance ID", Placeholder: "my-instance", Required: true},
					{Label: "Display Name", Placeholder: "My Instance", Required: true},
					{Label: "Config", Placeholder: "regional-us-central1", Required: true},
					{Label: "Node Count", Placeholder: "1", Default: "1"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.viewState = ViewDetail
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

		if s.viewState == ViewDetail {
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedInstance = nil
				return s, nil
			case "u":
				if s.selectedInstance != nil {
					s.updateForm = newInstanceUpdateForm(*s.selectedInstance)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // Grant IAM binding
				if s.selectedInstance != nil {
					s.iamForm = newInstanceIAMForm()
					s.viewState = ViewIAMForm
				}
				return s, nil
			case "e": // Execute SQL (read-only, data-plane)
				if s.selectedInstance != nil {
					s.queryForm = newQueryForm(*s.selectedInstance)
					s.viewState = ViewQuery
				}
				return s, nil
			}
		}

		if s.viewState == ViewQuery {
			result, formCmd := s.queryForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				database := s.queryForm.Value("Database ID")
				stmt := s.queryForm.Value("SQL Statement")
				return s, tea.Batch(s.executeQueryCmd(*s.selectedInstance, database, stmt), s.spinner.Start(""))
			}
			return s, formCmd
		}

		if s.viewState == ViewQueryResult {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
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

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedInstance != nil {
					actionCmd = s.deleteInstanceCmd(*s.selectedInstance)
				} else if s.pendingAction == "grant" && s.selectedInstance != nil {
					actionCmd = s.addIAMBindingCmd(*s.selectedInstance, s.pendingIAMRole, s.pendingIAMMember)
				}
				s.viewState = s.actionSource
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
		}
	}
	return s, nil
}

// -----------------------------------------------------------------------------
// Data & Helpers
// -----------------------------------------------------------------------------

// createInstanceCmd fires the CreateInstance API call using the current form values.
func (s *Service) createInstanceCmd() tea.Cmd {
	id := s.createForm.Value("Instance ID")
	displayName := s.createForm.Value("Display Name")
	config := s.createForm.Value("Config")
	nodeCount, err := strconv.Atoi(s.createForm.Value("Node Count"))
	if err != nil || nodeCount <= 0 {
		nodeCount = 1
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateInstance(s.projectID, id, displayName, config, nodeCount); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Instance %s created", id)}
	}
}

// updateInstanceCmd fires the UpdateInstanceNodeCount API call using the
// current update-form value.
func (s *Service) updateInstanceCmd(inst Instance) tea.Cmd {
	nodeCount, err := strconv.Atoi(s.updateForm.Value("Node Count"))
	if err != nil || nodeCount <= 0 {
		nodeCount = inst.NodeCount
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateInstanceNodeCount(s.projectID, inst.Name, nodeCount); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating instance %s to %d nodes...", inst.Name, nodeCount)}
	}
}

// addIAMBindingCmd fires the AddInstanceIAMBinding API call.
func (s *Service) addIAMBindingCmd(inst Instance, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddInstanceIAMBinding(s.projectID, inst.Name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on %s", role, member, inst.Name)}
	}
}

// executeQueryCmd runs a read-only SQL statement against database on inst.
func (s *Service) executeQueryCmd(inst Instance, database, sqlStatement string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return queryResultMsg{err: fmt.Errorf("client not initialized")}
		}
		result, err := s.client.ExecuteQuery(s.projectID, inst.Name, database, sqlStatement)
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
	if result == nil {
		s.queryTable = nil
		return
	}
	columns := make([]table.Column, len(result.Columns))
	for i, name := range result.Columns {
		width := len(name) + 4
		if width < 12 {
			width = 12
		}
		if width > 30 {
			width = 30
		}
		columns[i] = table.Column{Title: name, Width: width}
	}

	rows := make([]table.Row, len(result.Rows))
	for i, r := range result.Rows {
		rows[i] = table.Row(r.Values)
	}

	t := components.NewStandardTable(columns)
	t.SetRows(rows)
	s.queryTable = t
}

// deleteInstanceCmd triggers deletion of the given Spanner instance
func (s *Service) deleteInstanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteInstance(s.projectID, inst.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting instance %s...", inst.Name)}
	}
}

func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("spanner:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Instance); ok {
					return instancesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListInstances(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return instancesMsg(items)
	}
}

func (s *Service) updateTable(items []Instance) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		capacity := fmt.Sprintf("%d Nodes", item.NodeCount)
		if item.NodeCount == 0 && item.ProcessingUnits > 0 {
			capacity = fmt.Sprintf("%d PUs", item.ProcessingUnits)
		}

		rows[i] = table.Row{
			item.Name,
			capacity,
			item.Config,
			item.State,
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
		return components.ContainsMatch(inst.Name, inst.Config, inst.State)(q)
	})
}
