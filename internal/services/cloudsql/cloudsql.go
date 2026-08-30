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
	ViewDatabases
	ViewDatabaseCreate
	ViewUsers
	ViewUserCreate
	ViewBackups
	ViewClone
)

// newDatabaseCreateForm builds the FormModel for creating a new database on
// a Cloud SQL instance, matching `gcloud sql databases create`.
func newDatabaseCreateForm(inst Instance) components.FormModel {
	return components.NewForm("Create Database: "+inst.Name, []components.FormField{
		{Label: "Name", Placeholder: "my_database", Required: true},
	})
}

// newUserCreateForm builds the FormModel for creating a new database user
// on a Cloud SQL instance, matching `gcloud sql users create`.
func newUserCreateForm(inst Instance) components.FormModel {
	return components.NewForm("Create User: "+inst.Name, []components.FormField{
		{Label: "Name", Placeholder: "my_user", Required: true},
		{Label: "Password", Required: true},
	})
}

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

// newCloneForm builds the FormModel for cloning a Cloud SQL instance,
// matching `gcloud sql instances clone SOURCE DEST [--point-in-time=...]`.
// Point In Time is optional (RFC 3339); leaving it blank clones the
// instance's current state -- there is no separate PITR-restore RPC in the
// Cloud SQL Admin API, just a clone with CloneContext.PointInTime set.
func newCloneForm(inst Instance) components.FormModel {
	return components.NewForm("Clone Instance: "+inst.Name, []components.FormField{
		{Label: "Destination Name", Placeholder: inst.Name + "-clone", Required: true},
		{Label: "Point In Time (optional, RFC3339)", Placeholder: "2024-01-15T00:00:00Z"},
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

	// pendingJobAction/pendingJobName snapshot the instance-level action and
	// target name at confirmation time (before pendingAction/selectedInstance
	// are reset), so the actionResultMsg handler can still record a job for
	// it once the async call resolves.
	pendingJobAction string
	pendingJobName   string

	// Create State
	createForm components.FormModel

	// Update State
	updateForm components.FormModel

	// Query State (read-only "Execute SQL" data-plane feature)
	queryForm   components.FormModel
	queryResult *QueryResult
	queryErr    error
	queryTable  *components.StandardTable

	// Databases sub-view state
	databases        []Database
	dbTable          *components.StandardTable
	dbCreateForm     components.FormModel
	selectedDatabase *Database

	// Users sub-view state
	users          []DBUser
	userTable      *components.StandardTable
	userCreateForm components.FormModel
	selectedDBUser *DBUser

	// Backups sub-view state (read-only)
	backups     []Backup
	backupTable *components.StandardTable

	// Clone state
	cloneForm        components.FormModel
	pendingCloneDest string
	pendingClonePITR string

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

	dbTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 30},
		{Title: "Charset", Width: 15},
		{Title: "Collation", Width: 20},
	})
	userTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 30},
		{Title: "Host", Width: 20},
		{Title: "Type", Width: 15},
	})
	backupTable := components.NewStandardTable([]table.Column{
		{Title: "ID", Width: 15},
		{Title: "Status", Width: 15},
		{Title: "Type", Width: 15},
		{Title: "Start Time", Width: 22},
		{Title: "End Time", Width: 22},
	})

	svc := &Service{
		table:       t,
		dbTable:     dbTable,
		userTable:   userTable,
		backupTable: backupTable,
		filter:      components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:     components.NewSpinner(),
		viewState:   ViewList,
		cache:       cache,
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
		return "r:Refresh  /:Filter  s:Start  x:Stop  t:Restart  l:Logs  Ent:Detail  n:Create  u:Update  e:Execute SQL  d:Delete"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  s:Start  x:Stop  t:Restart  F:Failover  P:Promote Replica  S:Switchover  C:Clone  D:Databases  U:Users  B:Backups  u:Update  e:Execute SQL  d:Delete"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewQuery || s.viewState == ViewDatabaseCreate || s.viewState == ViewUserCreate || s.viewState == ViewClone {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	}
	if s.viewState == ViewQueryResult || s.viewState == ViewBackups {
		return "Esc/q:Back"
	}
	if s.viewState == ViewDatabases {
		return "Esc/q:Back  r:Refresh  n:Create  d:Delete"
	}
	if s.viewState == ViewUsers {
		return "Esc/q:Back  r:Refresh  n:Create  d:Delete"
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
type databasesMsg struct {
	databases []Database
	err       error
}
type usersMsg struct {
	users []DBUser
	err   error
}
type backupsMsg struct {
	backups []Backup
	err     error
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

	case databasesMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.databases = msg.databases
		s.updateDatabaseTable(msg.databases)
		s.viewState = ViewDatabases
		return s, nil

	case usersMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.users = msg.users
		s.updateUserTable(msg.users)
		s.viewState = ViewUsers
		return s, nil

	case backupsMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.backups = msg.backups
		s.updateBackupTable(msg.backups)
		s.viewState = ViewBackups
		return s, nil

	case actionResultMsg:
		// Databases/Users sub-view actions re-fetch just that sub-list on
		// success instead of the generic instance-list Refresh() below.
		if s.pendingAction == "db-create" || s.pendingAction == "db-delete" {
			action := "create"
			name := s.dbCreateForm.Value("Name")
			if s.pendingAction == "db-delete" {
				action = "delete"
				if s.selectedDatabase != nil {
					name = s.selectedDatabase.Name
				}
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "database",
				Name: name, Action: action, Status: status, Error: errStr,
			})
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedInstance != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchDatabasesCmd(*s.selectedInstance),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "user-create" || s.pendingAction == "user-delete" {
			action := "create"
			name := s.userCreateForm.Value("Name")
			if s.pendingAction == "user-delete" {
				action = "delete"
				if s.selectedDBUser != nil {
					name = s.selectedDBUser.Name
				}
			}
			status, errStr := core.JobSuccess, ""
			if msg.err != nil {
				status, errStr = core.JobFailed, msg.err.Error()
			}
			core.RecordJob(core.Job{
				Service: s.ShortName(), ProjectID: s.projectID, Resource: "user",
				Name: name, Action: action, Status: status, Error: errStr,
			})
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedInstance != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchUsersCmd(*s.selectedInstance),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		{
			// Instance-level lifecycle (start/stop/restart/failover/
			// promote-replica/switchover/clone/delete) actions carry their
			// job action/name via pendingJobAction/pendingJobName, snapshotted
			// at confirmation time; create/update instance submit directly
			// from their forms without going through pendingAction.
			action := s.pendingJobAction
			name := s.pendingJobName
			if s.viewState == ViewCreate {
				action = "create"
				name = s.createForm.Value("Name")
			} else if s.viewState == ViewUpdate {
				action = "update"
				if s.selectedInstance != nil {
					name = s.selectedInstance.Name
				}
			}
			if action != "" {
				status, errStr := core.JobSuccess, ""
				if msg.err != nil {
					status, errStr = core.JobFailed, msg.err.Error()
				}
				core.RecordJob(core.Job{
					Service: s.ShortName(), ProjectID: s.projectID, Resource: "instance",
					Name: name, Action: action, Status: status, Error: errStr,
				})
			}
			s.pendingJobAction = ""
			s.pendingJobName = ""
		}
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
			case "n": // Create
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
			case "F": // Failover (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "failover"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "P": // Promote Replica (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "promote-replica"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "S": // Switchover (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "switchover"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "C": // Clone
				if s.selectedInstance != nil {
					s.cloneForm = newCloneForm(*s.selectedInstance)
					s.viewState = ViewClone
				}
				return s, nil
			case "D": // Databases sub-view
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchDatabasesCmd(*s.selectedInstance))
				}
				return s, nil
			case "U": // Users sub-view
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchUsersCmd(*s.selectedInstance))
				}
				return s, nil
			case "B": // Backups sub-view (read-only)
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchBackupsCmd(*s.selectedInstance))
				}
				return s, nil
			}

		case ViewDatabases:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "r":
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchDatabasesCmd(*s.selectedInstance))
				}
			case "n": // Create
				if s.selectedInstance != nil {
					s.dbCreateForm = newDatabaseCreateForm(*s.selectedInstance)
					s.viewState = ViewDatabaseCreate
				}
				return s, nil
			case "d": // Delete (Confirm)
				if idx := s.dbTable.Cursor(); idx >= 0 && idx < len(s.databases) {
					s.selectedDatabase = &s.databases[idx]
					s.pendingAction = "db-delete"
					s.actionSource = ViewDatabases
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.dbTable.Update(msg)
			s.dbTable = updatedTable
			return s, cmd

		case ViewDatabaseCreate:
			result, fcmd := s.dbCreateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDatabases
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				s.pendingAction = "db-create"
				s.viewState = ViewDatabases
				return s, s.createDatabaseCmd(*s.selectedInstance, s.dbCreateForm.Value("Name"))
			}
			return s, fcmd

		case ViewUsers:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "r":
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchUsersCmd(*s.selectedInstance))
				}
			case "n": // Create
				if s.selectedInstance != nil {
					s.userCreateForm = newUserCreateForm(*s.selectedInstance)
					s.viewState = ViewUserCreate
				}
				return s, nil
			case "d": // Delete (Confirm)
				if idx := s.userTable.Cursor(); idx >= 0 && idx < len(s.users) {
					s.selectedDBUser = &s.users[idx]
					s.pendingAction = "user-delete"
					s.actionSource = ViewUsers
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
			var updatedUserTable *components.StandardTable
			updatedUserTable, cmd = s.userTable.Update(msg)
			s.userTable = updatedUserTable
			return s, cmd

		case ViewUserCreate:
			result, fcmd := s.userCreateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewUsers
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				s.pendingAction = "user-create"
				s.viewState = ViewUsers
				return s, s.createUserCmd(*s.selectedInstance, s.userCreateForm.Value("Name"), s.userCreateForm.Value("Password"))
			}
			return s, fcmd

		case ViewBackups:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "r":
				if s.selectedInstance != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchBackupsCmd(*s.selectedInstance))
				}
			}
			var updatedBackupTable *components.StandardTable
			updatedBackupTable, cmd = s.backupTable.Update(msg)
			s.backupTable = updatedBackupTable
			return s, cmd

		case ViewClone:
			result, fcmd := s.cloneForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				s.pendingCloneDest = s.cloneForm.Value("Destination Name")
				s.pendingClonePITR = s.cloneForm.Value("Point In Time (optional, RFC3339)")
				s.pendingAction = "clone"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
			}
			return s, fcmd

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
				instanceNameBeforeDelete := ""
				if s.selectedInstance != nil {
					instanceNameBeforeDelete = s.selectedInstance.Name
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
				case "failover":
					actionCmd = s.failoverInstanceCmd(*s.selectedInstance)
				case "promote-replica":
					actionCmd = s.promoteReplicaCmd(*s.selectedInstance)
				case "switchover":
					actionCmd = s.switchoverInstanceCmd(*s.selectedInstance)
				case "clone":
					if s.selectedInstance != nil {
						actionCmd = s.cloneInstanceCmd(*s.selectedInstance, s.pendingCloneDest, s.pendingClonePITR)
					}
					s.pendingCloneDest = ""
					s.pendingClonePITR = ""
				case "db-delete":
					if s.selectedInstance != nil && s.selectedDatabase != nil {
						actionCmd = s.deleteDatabaseCmd(*s.selectedInstance, *s.selectedDatabase)
					}
				case "user-delete":
					if s.selectedInstance != nil && s.selectedDBUser != nil {
						actionCmd = s.deleteUserCmd(*s.selectedInstance, *s.selectedDBUser)
					}
				}
				// Snapshot the instance-level action/name for job recording
				// before pendingAction/selectedInstance are reset below —
				// db-delete/user-delete record against their own
				// selectedDatabase/selectedDBUser instead, so skip those.
				if s.pendingAction != "db-delete" && s.pendingAction != "user-delete" && actionCmd != nil {
					jobAction := s.pendingAction
					if jobAction == "delete-confirm2" {
						jobAction = "delete"
					}
					s.pendingJobAction = jobAction
					if s.selectedInstance != nil {
						s.pendingJobName = s.selectedInstance.Name
					} else {
						s.pendingJobName = instanceNameBeforeDelete
					}
				}
				s.viewState = s.actionSource
				// "db-delete"/"user-delete" are reset by the actionResultMsg
				// handler once the async call resolves, so it knows to
				// re-fetch that sub-list on success.
				if s.pendingAction != "db-delete" && s.pendingAction != "user-delete" {
					s.pendingAction = ""
				}
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

	if s.viewState == ViewDatabases {
		return s.renderDatabasesView()
	}

	if s.viewState == ViewDatabaseCreate {
		return s.dbCreateForm.View()
	}

	if s.viewState == ViewUsers {
		return s.renderUsersView()
	}

	if s.viewState == ViewUserCreate {
		return s.userCreateForm.View()
	}

	if s.viewState == ViewBackups {
		return s.renderBackupsView()
	}

	if s.viewState == ViewClone {
		return s.cloneForm.View()
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
	s.selectedDatabase = nil
	s.selectedDBUser = nil
	s.pendingAction = ""
	s.pendingCloneDest = ""
	s.pendingClonePITR = ""
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

func (s *Service) updateDatabaseTable(dbs []Database) {
	rows := make([]table.Row, len(dbs))
	for i, d := range dbs {
		rows[i] = table.Row{d.Name, d.Charset, d.Collation}
	}
	s.dbTable.SetRows(rows)
}

func (s *Service) updateUserTable(users []DBUser) {
	rows := make([]table.Row, len(users))
	for i, u := range users {
		rows[i] = table.Row{u.Name, u.Host, u.Type}
	}
	s.userTable.SetRows(rows)
}

func (s *Service) updateBackupTable(backups []Backup) {
	rows := make([]table.Row, len(backups))
	for i, b := range backups {
		rows[i] = table.Row{fmt.Sprintf("%d", b.ID), b.Status, b.Type, b.StartTime, b.EndTime}
	}
	s.backupTable.SetRows(rows)
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

func (s *Service) failoverInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.FailoverInstance(s.projectID, i.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Failing over instance %s...", i.Name)}
	}
}

func (s *Service) promoteReplicaCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.PromoteReplica(s.projectID, i.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Promoting replica %s...", i.Name)}
	}
}

func (s *Service) switchoverInstanceCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.SwitchoverInstance(s.projectID, i.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Switching over instance %s...", i.Name)}
	}
}

func (s *Service) cloneInstanceCmd(i Instance, destName, pointInTime string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CloneInstance(s.projectID, i.Name, destName, pointInTime); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Cloning instance %s to %s...", i.Name, destName)}
	}
}

func (s *Service) fetchDatabasesCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return databasesMsg{err: fmt.Errorf("client not initialized")}
		}
		dbs, err := s.client.ListDatabases(s.projectID, i.Name)
		if err != nil {
			return databasesMsg{err: err}
		}
		return databasesMsg{databases: dbs}
	}
}

func (s *Service) createDatabaseCmd(i Instance, name string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateDatabase(s.projectID, i.Name, name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating database %s...", name)}
	}
}

func (s *Service) deleteDatabaseCmd(i Instance, db Database) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteDatabase(s.projectID, i.Name, db.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting database %s...", db.Name)}
	}
}

func (s *Service) fetchUsersCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return usersMsg{err: fmt.Errorf("client not initialized")}
		}
		users, err := s.client.ListUsers(s.projectID, i.Name)
		if err != nil {
			return usersMsg{err: err}
		}
		return usersMsg{users: users}
	}
}

func (s *Service) createUserCmd(i Instance, name, password string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateUser(s.projectID, i.Name, name, password); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating user %s...", name)}
	}
}

func (s *Service) deleteUserCmd(i Instance, u DBUser) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteUser(s.projectID, i.Name, u.Name, u.Host); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting user %s...", u.Name)}
	}
}

func (s *Service) fetchBackupsCmd(i Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return backupsMsg{err: fmt.Errorf("client not initialized")}
		}
		backups, err := s.client.ListBackups(s.projectID, i.Name)
		if err != nil {
			return backupsMsg{err: err}
		}
		return backupsMsg{backups: backups}
	}
}
