package filestore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewSnapshots
	ViewCreateSnapshot
	ViewRevert
)

// newSnapshotCreateForm builds the FormModel for creating a new snapshot of
// the currently-selected instance.
func newSnapshotCreateForm() components.FormModel {
	return components.NewForm("Create Snapshot", []components.FormField{
		{Label: "Snapshot ID", Placeholder: "my-snapshot", Required: true},
		{Label: "Description"},
	})
}

// newRevertForm builds the FormModel for reverting an instance to a prior
// snapshot, matching `gcloud filestore instances revert --snapshot`.
func newRevertForm(inst Instance) components.FormModel {
	return components.NewForm("Revert Instance: "+inst.Name, []components.FormField{
		{Label: "Snapshot ID", Placeholder: "my-snapshot", Required: true},
	})
}

// newInstanceUpdateForm builds the FormModel for resizing an instance's
// first file share, seeded with its current capacity. Multi-share
// instances only have their first share resized here; revert and
// promote/pause/resume-replica are out of scope.
func newInstanceUpdateForm(inst Instance) components.FormModel {
	shareName := "share1"
	capacity := int64(0)
	if len(inst.FileShares) > 0 {
		shareName = inst.FileShares[0].Name
		capacity = inst.FileShares[0].CapacityGB
	}
	return components.NewForm("Update Filestore Instance: "+inst.Name, []components.FormField{
		{Label: "File Share Name", Default: shareName, Required: true},
		{Label: "Capacity GB", Default: fmt.Sprintf("%d", capacity), Required: true},
	})
}

type instancesMsg []Instance
type snapshotsMsg []Snapshot
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g.
// instance creation).
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
	selectedSnapshot *Snapshot

	snapshots     []Snapshot
	snapshotTable *components.StandardTable

	createForm         components.FormModel
	updateForm         components.FormModel
	snapshotCreateForm components.FormModel
	revertForm         components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "promote-replica", "revert", "delete-snapshot"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 22},
		{Title: "Zone/Region", Width: 15},
		{Title: "Tier", Width: 14},
		{Title: "Capacity", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Network", Width: 16},
	}

	t := components.NewStandardTable(columns)

	snapCols := []table.Column{
		{Title: "Name", Width: 22},
		{Title: "State", Width: 12},
		{Title: "Created", Width: 22},
		{Title: "Description", Width: 30},
	}
	snapTable := components.NewStandardTable(snapCols)

	svc := &Service{
		table:         t,
		snapshotTable: snapTable,
		filter:        components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:       components.NewSpinner(),
		viewState:     ViewList,
		cache:         cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredInstances, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Filestore"
}

func (s *Service) ShortName() string {
	return "filestore"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Instance"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  d:Delete  s:Snapshots  p:Promote Replica  v:Revert"
	}
	if s.viewState == ViewSnapshots {
		return "Esc/q:Back  c:Create Snapshot  x:Delete"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewCreateSnapshot || s.viewState == ViewRevert {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	return ""
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
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchInstancesCmd(false),
		s.tick(),
	)
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

	case snapshotsMsg:
		s.spinner.Stop()
		s.snapshots = msg
		s.updateSnapshotTable()
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "delete" {
			s.pendingAction = ""
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
		if s.pendingAction == "create-snapshot" || s.pendingAction == "delete-snapshot" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.fetchSnapshotsCmd(),
			)
		}
		// Remaining outcomes share this generic path: create-instance,
		// update-instance, promote-replica, revert. pendingAction (still set
		// for the latter two) and viewState (ViewUpdate for update)
		// disambiguate which one this is.
		s.pendingAction = ""
		if msg.err != nil {
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		if s.viewState == ViewUpdate {
			s.viewState = ViewDetail
		}
		if msg.msg != "" {
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

		if s.viewState == ViewList {
			result := s.filterSession.HandleKey(msg)

			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
			}
		}

		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("New Filestore Instance", []components.FormField{
					{Label: "Instance ID", Required: true},
					{Label: "Zone", Required: true},
					{Label: "Tier", Default: "BASIC_HDD", Required: true},
					{Label: "Capacity GB", Default: "1024", Required: true},
					{Label: "File Share Name", Default: "share1", Required: true},
					{Label: "Network", Default: "default", Required: true},
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
			case "s": // Snapshots
				if s.selectedInstance != nil {
					s.viewState = ViewSnapshots
					return s, tea.Batch(s.fetchSnapshotsCmd(), s.spinner.Start(""))
				}
				return s, nil
			case "p": // Promote replica (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "promote-replica"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "v": // Revert to snapshot
				if s.selectedInstance != nil {
					s.revertForm = newRevertForm(*s.selectedInstance)
					s.viewState = ViewRevert
				}
				return s, nil
			}
		}

		if s.viewState == ViewSnapshots {
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewDetail
				return s, nil
			case "c": // Create snapshot
				s.snapshotCreateForm = newSnapshotCreateForm()
				s.viewState = ViewCreateSnapshot
				return s, nil
			case "x": // Delete snapshot (Confirm)
				if idx := s.snapshotTable.Cursor(); idx >= 0 && idx < len(s.snapshots) {
					s.selectedSnapshot = &s.snapshots[idx]
					s.pendingAction = "delete-snapshot"
					s.actionSource = ViewSnapshots
					s.viewState = ViewConfirmation
					return s, nil
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.snapshotTable.Update(msg)
			s.snapshotTable = updatedTable
			return s, cmd
		}

		if s.viewState == ViewCreateSnapshot {
			result, formCmd := s.snapshotCreateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewSnapshots
				return s, nil
			}
			if result.Submitted {
				s.pendingAction = "create-snapshot"
				s.viewState = ViewSnapshots
				return s, s.createSnapshotCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewRevert {
			result, formCmd := s.revertForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				s.pendingAction = "revert"
				s.viewState = ViewDetail
				return s, s.revertInstanceCmd(*s.selectedInstance)
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				switch {
				case s.pendingAction == "delete" && s.selectedInstance != nil:
					actionCmd = s.deleteInstanceCmd(*s.selectedInstance)
				case s.pendingAction == "promote-replica" && s.selectedInstance != nil:
					actionCmd = s.promoteReplicaCmd(*s.selectedInstance)
				case s.pendingAction == "delete-snapshot" && s.selectedSnapshot != nil:
					actionCmd = s.deleteSnapshotCmd(*s.selectedSnapshot)
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
// Views
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Instances")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewSnapshots {
		return s.renderSnapshotsView()
	}

	if s.viewState == ViewCreateSnapshot {
		return s.snapshotCreateForm.View()
	}

	if s.viewState == ViewRevert {
		return s.revertForm.View()
	}

	return s.renderListView()
}

// renderSnapshotsView renders the scrollable list of an instance's snapshots.
func (s *Service) renderSnapshotsView() string {
	if s.selectedInstance == nil {
		return "No instance selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Instances",
		s.selectedInstance.Name,
		"Snapshots",
	)
	if len(s.snapshots) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("snapshots"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.snapshotTable.View())
}

// renderConfirmation renders the confirmation dialog for the pending
// action: instance deletion, replica promotion, or snapshot deletion.
func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "promote-replica":
		if s.selectedInstance == nil {
			return "Error: No instance selected"
		}
		return components.RenderConfirmationWithMessage(
			"promote", s.selectedInstance.Name, "instance",
			fmt.Sprintf("Promote replica instance %s to active?", s.selectedInstance.Name),
		)
	case "delete-snapshot":
		if s.selectedSnapshot == nil {
			return "Error: No snapshot selected"
		}
		return components.RenderConfirmation("delete", s.selectedSnapshot.Name, "snapshot")
	default:
		if s.selectedInstance == nil {
			return "Error: No instance selected"
		}
		return components.RenderConfirmationWithMessage(
			s.pendingAction,
			s.selectedInstance.Name,
			"instance",
			fmt.Sprintf("Are you sure you want to DELETE instance %s? This destroys all file shares and their data.", s.selectedInstance.Name),
		)
	}
}

// createInstanceCmd fires the CreateInstance API call using the current
// createForm values.
func (s *Service) createInstanceCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := InstanceCreateOpts{
		InstanceID: v["Instance ID"],
		Zone:       v["Zone"],
		Tier:       v["Tier"],
		CapacityGB: v["Capacity GB"],
		ShareName:  v["File Share Name"],
		Network:    v["Network"],
	}
	s.viewState = ViewList
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "instance",
			Name: opts.InstanceID, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateInstance(s.projectID, opts)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating instance %s...", opts.InstanceID)}
	}
}

// updateInstanceCmd fires the UpdateInstanceCapacity API call using the
// current update-form values.
func (s *Service) updateInstanceCmd(inst Instance) tea.Cmd {
	shareName := s.updateForm.Value("File Share Name")
	capacity, err := strconv.ParseInt(s.updateForm.Value("Capacity GB"), 10, 64)
	if err != nil || capacity <= 0 {
		if len(inst.FileShares) > 0 {
			capacity = inst.FileShares[0].CapacityGB
		}
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "instance",
			Name: inst.Name, Action: "update",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.UpdateInstanceCapacity(inst.FullName, shareName, capacity)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resizing instance %s...", inst.Name)}
	}
}

// deleteInstanceCmd triggers deletion of the given Filestore instance
func (s *Service) deleteInstanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "instance",
			Name: inst.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteInstance(inst.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting instance %s...", inst.Name)}
	}
}

// promoteReplicaCmd promotes a standby replica instance to active.
func (s *Service) promoteReplicaCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "instance",
			Name: inst.Name, Action: "promote",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.PromoteReplica(inst.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Promoting replica %s...", inst.Name)}
	}
}

// revertInstanceCmd fires the RevertInstance API call using the current
// revertForm value.
func (s *Service) revertInstanceCmd(inst Instance) tea.Cmd {
	snapshotID := s.revertForm.Value("Snapshot ID")
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "instance",
			Name: inst.Name, Action: "revert",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.RevertInstance(inst.FullName, snapshotID)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Reverting instance %s to snapshot %s...", inst.Name, snapshotID)}
	}
}

// createSnapshotCmd fires the CreateSnapshot API call using the current
// snapshotCreateForm values.
func (s *Service) createSnapshotCmd() tea.Cmd {
	v := s.snapshotCreateForm.Values()
	snapshotID, description := v["Snapshot ID"], v["Description"]
	var instanceFullName string
	if s.selectedInstance != nil {
		instanceFullName = s.selectedInstance.FullName
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "snapshot",
			Name: snapshotID, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateSnapshot(instanceFullName, snapshotID, description)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating snapshot %s...", snapshotID)}
	}
}

// deleteSnapshotCmd triggers deletion of the given snapshot.
func (s *Service) deleteSnapshotCmd(snap Snapshot) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "snapshot",
			Name: snap.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteSnapshot(snap.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting snapshot %s...", snap.Name)}
	}
}

// fetchSnapshotsCmd fetches the snapshot list for the currently-selected instance.
func (s *Service) fetchSnapshotsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil || s.selectedInstance == nil {
			return errMsg(fmt.Errorf("no instance selected"))
		}
		snaps, err := s.client.ListSnapshots(s.selectedInstance.FullName)
		if err != nil {
			return errMsg(err)
		}
		return snapshotsMsg(snaps)
	}
}

// updateSnapshotTable rebuilds the snapshot table rows from s.snapshots.
func (s *Service) updateSnapshotTable() {
	rows := make([]table.Row, len(s.snapshots))
	for i, snap := range s.snapshots {
		rows[i] = table.Row{snap.Name, snap.State, snap.CreateTime, snap.Description}
	}
	s.snapshotTable.SetRows(rows)
}

func (s *Service) renderListView() string {
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
		content.WriteString(components.EmptyState("filestore instances"))
		return content.String()
	}

	content.WriteString(s.table.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("filestore_instances:%s", s.projectID)

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
			fmt.Sprintf("%d GB", item.CapacityGB),
			item.State,
			item.Network,
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
		return components.ContainsMatch(inst.Name, inst.Location, inst.Tier, inst.State, inst.Network)(q)
	})
}
