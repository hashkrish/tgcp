package redis

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
	ViewExport
	ViewImport
)

// newExportForm builds the FormModel for exporting an instance's data to GCS.
func newExportForm(inst Instance) components.FormModel {
	return components.NewForm("Export Instance: "+inst.Name, []components.FormField{
		{Label: "Output GCS URI", Placeholder: "gs://my-bucket/dump.rdb", Required: true},
	})
}

// newImportForm builds the FormModel for importing a GCS RDB file into an instance.
func newImportForm(inst Instance) components.FormModel {
	return components.NewForm("Import Instance: "+inst.Name, []components.FormField{
		{Label: "Input GCS URI", Placeholder: "gs://my-bucket/dump.rdb", Required: true},
	})
}

// newInstanceUpdateForm builds the FormModel for updating a Redis instance's
// memory size, seeded with the current value. Version "upgrade" and
// "reschedule-maintenance" are separate, higher-risk RPCs and are
// intentionally out of scope.
func newInstanceUpdateForm(inst Instance) components.FormModel {
	return components.NewForm("Update Redis Instance: "+inst.Name, []components.FormField{
		{Label: "Memory Size GB", Default: strconv.Itoa(inst.MemorySizeGb), Placeholder: "1", Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

type instancesMsg []Instance
type errMsg error

// actionResultMsg carries the result of an async action (e.g. instance creation)
type actionResultMsg struct {
	err error
	msg string
}

// authStringMsg carries the result of a GetAuthString call.
type authStringMsg struct {
	authString string
	err        error
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
	exportForm components.FormModel
	importForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "reschedule-maintenance"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Location", Width: 15},
		{Title: "Tier", Width: 15},
		{Title: "Capacity", Width: 10},
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
	return "Memorystore (Redis)"
}

func (s *Service) ShortName() string {
	return "redis"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Instance  u:Update"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  f:Failover  d:Delete"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
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

	case actionResultMsg:
		if s.pendingAction == "delete" || s.pendingAction == "failover" || s.pendingAction == "reschedule-maintenance" {
			action := s.pendingAction
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
				Name: instName, Action: action, Status: status, Error: errStr,
			})
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				s.selectedInstance = nil
				s.viewState = ViewList
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.viewState == ViewExport || s.viewState == ViewImport {
			action := "export"
			if s.viewState == ViewImport {
				action = "import"
			}
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
				Name: instName, Action: action, Status: status, Error: errStr,
			})
			if msg.err != nil {
				if s.viewState == ViewExport {
					s.exportForm.SubmitErr = msg.err.Error()
				} else {
					s.importForm.SubmitErr = msg.err.Error()
				}
				return s, nil
			}
			s.viewState = ViewDetail
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
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

	case authStringMsg:
		s.spinner.Stop()
		return s, func() tea.Msg {
			if msg.err != nil {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
			return core.ToastMsg{Message: fmt.Sprintf("AUTH string: %s", msg.authString), Type: core.ToastSuccess}
		}

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
				s.viewState = ViewList
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
				s.createForm = components.NewForm("Create Redis Instance", []components.FormField{
					{Label: "Instance ID", Placeholder: "my-redis-instance", Required: true},
					{Label: "Region", Placeholder: "us-central1", Required: true},
					{Label: "Tier", Placeholder: "BASIC", Default: "BASIC"},
					{Label: "Memory Size GB", Placeholder: "1", Default: "1"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.viewState = ViewDetail
				}
			case "u":
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.updateForm = newInstanceUpdateForm(*s.selectedInstance)
					s.viewState = ViewUpdate
					return s, nil
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
			case "f": // Failover (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "failover"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "M": // Reschedule maintenance to now (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "reschedule-maintenance"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "e": // Export to GCS
				if s.selectedInstance != nil {
					s.exportForm = newExportForm(*s.selectedInstance)
					s.viewState = ViewExport
				}
				return s, nil
			case "i": // Import from GCS
				if s.selectedInstance != nil {
					s.importForm = newImportForm(*s.selectedInstance)
					s.viewState = ViewImport
				}
				return s, nil
			case "a": // Get auth string
				if s.selectedInstance != nil {
					return s, tea.Batch(s.getAuthStringCmd(*s.selectedInstance), s.spinner.Start(""))
				}
				return s, nil
			}
		}

		if s.viewState == ViewExport {
			result, formCmd := s.exportForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.exportInstanceCmd(*s.selectedInstance, s.exportForm.Value("Output GCS URI"))
			}
			return s, formCmd
		}

		if s.viewState == ViewImport {
			result, formCmd := s.importForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				return s, s.importInstanceCmd(*s.selectedInstance, s.importForm.Value("Input GCS URI"))
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.selectedInstance != nil {
					switch s.pendingAction {
					case "delete":
						actionCmd = s.deleteInstanceCmd(*s.selectedInstance)
					case "failover":
						actionCmd = s.failoverInstanceCmd(*s.selectedInstance)
					case "reschedule-maintenance":
						actionCmd = s.rescheduleMaintenanceCmd(*s.selectedInstance)
					}
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
	region := s.createForm.Value("Region")
	tier := s.createForm.Value("Tier")
	if tier == "" {
		tier = "BASIC"
	}
	memoryGb, err := strconv.ParseInt(s.createForm.Value("Memory Size GB"), 10, 64)
	if err != nil || memoryGb <= 0 {
		memoryGb = 1
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateInstance(s.projectID, id, region, tier, memoryGb); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating Redis instance %s...", id)}
	}
}

// updateInstanceCmd fires the UpdateInstanceMemorySize API call using the
// current form values, scoped to the instance selected before entering the
// update form.
func (s *Service) updateInstanceCmd(inst Instance) tea.Cmd {
	memoryGb, err := strconv.ParseInt(s.updateForm.Value("Memory Size GB"), 10, 64)
	if err != nil || memoryGb <= 0 {
		memoryGb = int64(inst.MemorySizeGb)
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateInstanceMemorySize(s.projectID, inst.Name, inst.Location, memoryGb); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating Redis instance %s...", inst.Name)}
	}
}

// rescheduleMaintenanceCmd reschedules inst's pending maintenance to now.
func (s *Service) rescheduleMaintenanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RescheduleMaintenance(s.projectID, inst.Name, inst.Location); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Rescheduling maintenance for %s...", inst.Name)}
	}
}

// exportInstanceCmd exports inst's data to gcsURI.
func (s *Service) exportInstanceCmd(inst Instance, gcsURI string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ExportInstance(s.projectID, inst.Name, inst.Location, gcsURI); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Export of %s to %s started", inst.Name, gcsURI)}
	}
}

// importInstanceCmd imports gcsURI into inst.
func (s *Service) importInstanceCmd(inst Instance, gcsURI string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ImportInstance(s.projectID, inst.Name, inst.Location, gcsURI); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Import into %s from %s started", inst.Name, gcsURI)}
	}
}

// getAuthStringCmd fetches inst's current AUTH string.
func (s *Service) getAuthStringCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return authStringMsg{err: fmt.Errorf("client not initialized")}
		}
		authString, err := s.client.GetAuthString(s.projectID, inst.Name, inst.Location)
		if err != nil {
			return authStringMsg{err: err}
		}
		return authStringMsg{authString: authString}
	}
}

// deleteInstanceCmd triggers deletion of the given Redis instance
func (s *Service) deleteInstanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteInstance(s.projectID, inst.Name, inst.Location); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting Redis instance %s...", inst.Name)}
	}
}

// failoverInstanceCmd triggers a failover of the given Redis instance to its
// current read replica.
func (s *Service) failoverInstanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.FailoverInstance(s.projectID, inst.Name, inst.Location); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Failing over Redis instance %s...", inst.Name)}
	}
}

func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("redis:%s", s.projectID)
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
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.Tier,
			fmt.Sprintf("%d GB", item.MemorySizeGb),
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
		return components.ContainsMatch(inst.Name, inst.Location, inst.Tier, inst.State)(q)
	})
}
