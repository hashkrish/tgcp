package bigtable

import (
	"context"
	"fmt"
	"strconv"
	"strings"
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
	ViewTables
	ViewCreateTable
	ViewIAMForm
	ViewUndeleteTable
	ViewRestoreTable
)

// newUndeleteTableForm builds the FormModel for undeleting a recently
// deleted table, matching `gcloud bigtable instances tables undelete`.
func newUndeleteTableForm() components.FormModel {
	return components.NewForm("Undelete Table", []components.FormField{
		{Label: "Table ID", Placeholder: "my-deleted-table", Required: true},
	})
}

// newRestoreTableForm builds the FormModel for restoring a table from a
// backup, matching `gcloud bigtable tables restore`. This package has no
// backup create/list flow, so the backup's fully qualified resource name
// must be supplied directly.
func newRestoreTableForm() components.FormModel {
	return components.NewForm("Restore Table from Backup", []components.FormField{
		{Label: "New Table ID", Placeholder: "my-restored-table", Required: true},
		{Label: "Backup Resource Name", Placeholder: "projects/P/instances/I/clusters/C/backups/B", Required: true},
	})
}

// newClusterUpdateForm builds the FormModel for resizing/autoscaling a
// cluster, seeded with the first cluster's current values. Cluster Name
// lets a multi-cluster instance target a cluster other than the first.
// Leaving Autoscaling Min Nodes at 0 keeps/sets a fixed node count from Num
// Nodes instead; setting it >0 switches the cluster to autoscaling using
// Autoscaling Max Nodes and Autoscaling CPU Target.
func newClusterUpdateForm(instance Instance, cluster Cluster) components.FormModel {
	return components.NewForm("Update Cluster: "+instance.Name, []components.FormField{
		{Label: "Cluster Name", Default: cluster.Name, Required: true},
		{Label: "Num Nodes", Default: strconv.Itoa(cluster.ServeNodes), Required: true, Validate: validatePositiveInt},
		{Label: "Autoscaling Min Nodes", Default: strconv.Itoa(cluster.AutoscalingMin), Placeholder: "0 = disabled (use fixed Num Nodes)"},
		{Label: "Autoscaling Max Nodes", Default: strconv.Itoa(cluster.AutoscalingMax)},
		{Label: "Autoscaling CPU Target", Default: strconv.Itoa(cluster.AutoscalingCpuTarget), Placeholder: "10-80"},
		{Label: "Instance Type", Default: instance.Type, Placeholder: "PRODUCTION (upgrade from DEVELOPMENT)"},
	})
}

func validatePositiveInt(v string) string {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return "must be a positive integer"
	}
	return ""
}

// newTableCreateForm builds the FormModel for creating a data-plane table.
func newTableCreateForm() components.FormModel {
	return components.NewForm("Create Table", []components.FormField{
		{Label: "Table ID", Placeholder: "my-table", Required: true},
		{Label: "Column Families", Placeholder: "cf1,cf2", Required: true},
	})
}

// newInstanceIAMForm builds the FormModel for granting an IAM role on an instance.
func newInstanceIAMForm() components.FormModel {
	return components.NewForm("Add IAM Binding", []components.FormField{
		{Label: "Role", Placeholder: "roles/bigtable.user", Required: true},
		{Label: "Member", Placeholder: "user:name@example.com", Required: true},
	})
}

type instancesMsg []Instance
type clustersMsg struct {
	instanceID string
	clusters   []Cluster
}
type tablesMsg struct {
	instanceID string
	tables     []TableInfo
}
type errMsg error

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
	clusters  []Cluster // For selected instance
	spinner   components.SpinnerModel
	err       error

	tablesTable         *components.StandardTable
	tablesFilter        components.FilterModel
	tablesFilterSession components.FilterSession[TableInfo]
	tables              []TableInfo // Data-plane tables for selected instance

	viewState        ViewState
	selectedInstance *Instance
	selectedTable    *TableInfo

	createForm        components.FormModel
	updateForm        components.FormModel
	tableCreateForm   components.FormModel
	iamForm           components.FormModel
	undeleteTableForm components.FormModel
	restoreTableForm  components.FormModel

	// pendingIAMRole/pendingIAMMember are captured at form-submit time so
	// the confirmation dialog and the actual API call use the same values
	// regardless of what the form fields hold later.
	pendingIAMRole   string
	pendingIAMMember string

	// Confirmation State
	pendingAction string    // "delete", "delete-table", "grant"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Display Name", Width: 30},
		{Title: "Type", Width: 15},
		{Title: "State", Width: 10},
	}

	t := components.NewStandardTable(columns)

	tableColumns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Column Families", Width: 40},
	}
	tt := components.NewStandardTable(tableColumns)

	svc := &Service{
		table:        t,
		filter:       components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:      components.NewSpinner(),
		viewState:    ViewList,
		cache:        cache,
		tablesTable:  tt,
		tablesFilter: components.NewFilterWithPlaceholder("Filter tables..."),
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredInstances, svc.updateTable)
	svc.tablesFilterSession = components.NewFilterSession(&svc.tablesFilter, svc.getFilteredTables, svc.updateTablesTable)
	return svc
}

func (s *Service) Name() string {
	return "Bigtable"
}

func (s *Service) ShortName() string {
	return "bigtable"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Instance"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewCreateTable || s.viewState == ViewIAMForm || s.viewState == ViewUndeleteTable || s.viewState == ViewRestoreTable {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update Cluster  d:Delete  t:Tables  i:Add IAM Binding"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewTables {
		return "Esc/q:Back  /:Filter  n:New Table  d:Delete Table  u:Undelete  R:Restore"
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
	s.clusters = nil
	s.tables = nil
	s.err = nil
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
	s.tablesTable.SetCursor(0)
	s.tablesFilter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.table.Focus()
	s.tablesTable.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
	s.tablesTable.Blur()
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

	case clustersMsg:
		// Discard results from a stale request (user navigated to a different instance since)
		if s.selectedInstance == nil || s.selectedInstance.Name != msg.instanceID {
			return s, nil
		}
		s.clusters = msg.clusters
		// We don't change state here, just store data for view

	case tablesMsg:
		s.spinner.Stop()
		// Discard results from a stale request (user navigated away since)
		if s.selectedInstance == nil || s.selectedInstance.Name != msg.instanceID {
			return s, nil
		}
		s.tables = msg.tables
		s.tablesFilterSession.Apply(s.tables)
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
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
			s.clusters = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.pendingAction == "delete-table" {
			s.pendingAction = ""
			tableName := ""
			if s.selectedTable != nil {
				tableName = s.selectedTable.Name
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "table",
				Name: tableName, Action: "delete", Status: status, Error: errStr,
			})
			if msg.err != nil {
				return s, func() tea.Msg { return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError} }
			}
			s.selectedTable = nil
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			if s.selectedInstance != nil {
				return s, tea.Batch(toast, s.fetchTablesCmd(s.selectedInstance.Name))
			}
			return s, toast
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
		if s.viewState == ViewCreateTable {
			tableID := s.tableCreateForm.Value("Table ID")
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "table",
				Name: tableID, Action: "create", Status: status, Error: errStr,
			})
			if msg.err != nil {
				s.tableCreateForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			s.viewState = ViewTables
			if s.selectedInstance != nil {
				return s, tea.Batch(toast, s.fetchTablesCmd(s.selectedInstance.Name))
			}
			return s, toast
		}
		if s.viewState == ViewUndeleteTable {
			tableID := s.undeleteTableForm.Value("Table ID")
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "table",
				Name: tableID, Action: "undelete", Status: status, Error: errStr,
			})
			if msg.err != nil {
				s.undeleteTableForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			s.viewState = ViewTables
			if s.selectedInstance != nil {
				return s, tea.Batch(toast, s.fetchTablesCmd(s.selectedInstance.Name))
			}
			return s, toast
		}
		if s.viewState == ViewRestoreTable {
			tableID := s.restoreTableForm.Value("New Table ID")
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "table",
				Name: tableID, Action: "restore", Status: status, Error: errStr,
			})
			if msg.err != nil {
				s.restoreTableForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			s.viewState = ViewTables
			if s.selectedInstance != nil {
				return s, tea.Batch(toast, s.fetchTablesCmd(s.selectedInstance.Name))
			}
			return s, toast
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
		s.tablesTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}
		if s.viewState == ViewTables {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.tablesTable.Update(msg)
			s.tablesTable = updatedTable
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
			if result.Submitted && s.selectedInstance != nil && len(s.clusters) > 0 {
				return s, s.updateClusterCmd(*s.selectedInstance)
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
				s.createForm = components.NewForm("Create Bigtable Instance", []components.FormField{
					{Label: "Instance ID", Placeholder: "my-instance", Required: true},
					{Label: "Display Name", Placeholder: "My Instance", Required: true},
					{Label: "Cluster ID", Placeholder: "my-instance-c1", Required: true},
					{Label: "Zone", Placeholder: "us-central1-a", Required: true},
					{Label: "Storage Type", Placeholder: "SSD", Default: "SSD"},
					{Label: "Num Nodes", Placeholder: "1", Default: "1"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.viewState = ViewDetail
					s.clusters = nil // Clear old
					return s, s.fetchClustersCmd(s.selectedInstance.Name)
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
				s.clusters = nil
				return s, nil
			case "u":
				if len(s.clusters) > 0 && s.selectedInstance != nil {
					s.updateForm = newClusterUpdateForm(*s.selectedInstance, s.clusters[0])
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
			case "t": // Tables (read-only data-plane drill-down)
				if s.selectedInstance != nil {
					s.viewState = ViewTables
					s.tables = nil
					return s, tea.Batch(s.spinner.Start(""), s.fetchTablesCmd(s.selectedInstance.Name))
				}
				return s, nil
			case "i": // Grant IAM binding
				if s.selectedInstance != nil {
					s.iamForm = newInstanceIAMForm()
					s.viewState = ViewIAMForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewTables {
			result := s.tablesFilterSession.HandleKey(msg)
			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
			}

			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewDetail
				s.tables = nil
				return s, nil
			case "n": // Create table
				if s.selectedInstance != nil {
					s.tableCreateForm = newTableCreateForm()
					s.viewState = ViewCreateTable
				}
				return s, nil
			case "d": // Delete table (Confirm)
				tables := s.getFilteredTables(s.tables, s.tablesFilter.Value())
				if idx := s.tablesTable.Cursor(); idx >= 0 && idx < len(tables) {
					s.selectedTable = &tables[idx]
					s.pendingAction = "delete-table"
					s.actionSource = ViewTables
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "u": // Undelete table
				s.undeleteTableForm = newUndeleteTableForm()
				s.viewState = ViewUndeleteTable
				return s, nil
			case "R": // Restore table from backup
				s.restoreTableForm = newRestoreTableForm()
				s.viewState = ViewRestoreTable
				return s, nil
			}

			var updatedTable *components.StandardTable
			updatedTable, cmd = s.tablesTable.Update(msg)
			s.tablesTable = updatedTable
			return s, cmd
		}

		if s.viewState == ViewUndeleteTable {
			result, formCmd := s.undeleteTableForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewTables
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.undeleteTableCmd(*s.selectedInstance, s.undeleteTableForm.Value("Table ID"))
			}
			return s, formCmd
		}

		if s.viewState == ViewRestoreTable {
			result, formCmd := s.restoreTableForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewTables
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.restoreTableCmd(*s.selectedInstance, s.restoreTableForm.Value("New Table ID"), s.restoreTableForm.Value("Backup Resource Name"))
			}
			return s, formCmd
		}

		if s.viewState == ViewCreateTable {
			result, formCmd := s.tableCreateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewTables
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.createTableCmd(*s.selectedInstance, s.tableCreateForm.Value("Table ID"), strings.Split(s.tableCreateForm.Value("Column Families"), ","))
			}
			return s, formCmd
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
				} else if s.pendingAction == "delete-table" && s.selectedInstance != nil && s.selectedTable != nil {
					actionCmd = s.deleteTableCmd(*s.selectedInstance, *s.selectedTable)
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
	instanceID := s.createForm.Value("Instance ID")
	displayName := s.createForm.Value("Display Name")
	clusterID := s.createForm.Value("Cluster ID")
	zone := s.createForm.Value("Zone")
	storageType := s.createForm.Value("Storage Type")
	if storageType == "" {
		storageType = "SSD"
	}
	numNodes := 1
	if v := s.createForm.Value("Num Nodes"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			numNodes = n
		}
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateInstance(s.projectID, instanceID, displayName, clusterID, zone, storageType, numNodes); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Instance %s created", instanceID)}
	}
}

// updateClusterCmd fires the node-count/autoscaling/instance-type API calls
// using the current update-form values: node resize or autoscaling config
// for the named cluster (Autoscaling Min Nodes > 0 switches to
// autoscaling), plus an instance-type change (e.g. DEVELOPMENT ->
// PRODUCTION "upgrade") if that field differs from the instance's current type.
func (s *Service) updateClusterCmd(inst Instance) tea.Cmd {
	clusterID := s.updateForm.Value("Cluster Name")
	numNodes, err := strconv.Atoi(s.updateForm.Value("Num Nodes"))
	if err != nil || numNodes <= 0 {
		numNodes = 1
	}
	autoMin, _ := strconv.Atoi(s.updateForm.Value("Autoscaling Min Nodes"))
	autoMax, _ := strconv.Atoi(s.updateForm.Value("Autoscaling Max Nodes"))
	autoCPU, _ := strconv.Atoi(s.updateForm.Value("Autoscaling CPU Target"))
	instanceType := s.updateForm.Value("Instance Type")

	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if autoMin > 0 {
			if err := s.client.SetClusterAutoscaling(s.projectID, inst.Name, clusterID, autoMin, autoMax, autoCPU); err != nil {
				return actionResultMsg{err: err}
			}
		} else if err := s.client.UpdateClusterNodes(s.projectID, inst.Name, clusterID, numNodes); err != nil {
			return actionResultMsg{err: err}
		}
		if instanceType != "" && instanceType != inst.Type {
			if err := s.client.UpdateInstanceType(s.projectID, inst.Name, instanceType); err != nil {
				return actionResultMsg{err: err}
			}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating cluster %s...", clusterID)}
	}
}

// createTableCmd fires the CreateTable API call.
func (s *Service) createTableCmd(inst Instance, tableID string, columnFamilies []string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateTable(s.projectID, inst.Name, tableID, columnFamilies); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Table %s created", tableID)}
	}
}

// deleteTableCmd fires the DeleteTable API call.
func (s *Service) deleteTableCmd(inst Instance, t TableInfo) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteTable(s.projectID, inst.Name, t.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting table %s...", t.Name)}
	}
}

// undeleteTableCmd fires the UndeleteTable API call.
func (s *Service) undeleteTableCmd(inst Instance, tableID string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UndeleteTable(s.projectID, inst.Name, tableID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Undeleting table %s...", tableID)}
	}
}

// restoreTableCmd fires the RestoreTable API call.
func (s *Service) restoreTableCmd(inst Instance, newTableID, backupName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RestoreTable(s.projectID, inst.Name, newTableID, backupName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Restoring table %s from backup...", newTableID)}
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

// deleteInstanceCmd triggers deletion of the given Bigtable instance
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
		key := fmt.Sprintf("bigtable:%s", s.projectID)
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

func (s *Service) fetchClustersCmd(instanceID string) tea.Cmd {
	return func() tea.Msg {
		// No cache for clusters loop (simplification for MVP)
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListClusters(s.projectID, instanceID)
		if err != nil {
			return errMsg(err)
		}
		return clustersMsg{instanceID: instanceID, clusters: items}
	}
}

// fetchTablesCmd lists the data-plane tables for the given instance
// (read-only; no cache, matching fetchClustersCmd's simplification for MVP).
func (s *Service) fetchTablesCmd(instanceID string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListTables(s.projectID, instanceID)
		if err != nil {
			return errMsg(err)
		}
		return tablesMsg{instanceID: instanceID, tables: items}
	}
}

func (s *Service) updateTablesTable(items []TableInfo) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		cf := "-"
		if len(item.ColumnFamilies) > 0 {
			cf = strings.Join(item.ColumnFamilies, ", ")
		}
		rows[i] = table.Row{
			item.Name,
			cf,
		}
	}
	s.tablesTable.SetRows(rows)
}

// getFilteredTables returns filtered tables based on the query string
func (s *Service) getFilteredTables(items []TableInfo, query string) []TableInfo {
	if query == "" {
		return items
	}
	return components.FilterSlice(items, query, func(t TableInfo, q string) bool {
		return components.ContainsMatch(append([]string{t.Name}, t.ColumnFamilies...)...)(q)
	})
}

func (s *Service) updateTable(items []Instance) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.DisplayName,
			item.Type,
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
		return components.ContainsMatch(inst.Name, inst.DisplayName, inst.Type, inst.State)(q)
	})
}
