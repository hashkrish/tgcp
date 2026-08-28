package gce

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

// newInstanceCreateForm builds the FormModel for creating a new VM instance.
func newInstanceCreateForm() components.FormModel {
	return components.NewForm("Create VM Instance", []components.FormField{
		{Label: "Name", Placeholder: "my-instance", Required: true},
		{Label: "Zone", Placeholder: "us-central1-a", Required: true},
		{Label: "Machine Type", Default: "e2-medium", Required: true},
		{Label: "Source Image", Default: "projects/debian-cloud/global/images/family/debian-12", Required: true},
		{Label: "Network", Default: "default", Required: true},
	})
}

// newInstanceUpdateForm builds the FormModel for updating a VM instance's
// network tags, seeded with its current tags (comma-separated). This is the
// simplest single-field Update flow for VM Instances: set-machine-type
// requires the instance to be stopped first, and label/metadata updates are
// separate fingerprinted calls, so both are intentionally out of scope.
func newInstanceUpdateForm(inst Instance) components.FormModel {
	return components.NewForm("Update VM Instance: "+inst.Name, []components.FormField{
		{Label: "Tags (comma-separated)", Default: strings.Join(inst.Tags, ",")},
	})
}

const CacheTTL = 30 * time.Second

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
	ViewUpdateGroup
)

// newGroupUpdateForm builds the FormModel for resizing a MIG's target
// size, seeded with its current value. set-autoscaling and update-instances
// (rolling replace/restart) are out of scope for this minimal Update flow.
func newGroupUpdateForm(g InstanceGroup) components.FormModel {
	return components.NewForm("Update MIG: "+g.Name, []components.FormField{
		{Label: "Target Size", Default: fmt.Sprintf("%d", g.TargetSize), Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return "must be a non-negative integer"
			}
			return ""
		}},
	})
}

// Tab distinguishes the Instances list from the Instance Groups (MIGs) list.
// Only ViewList is tab-aware — ViewDetail/ViewConfirmation remain exclusively
// about a selected Instance, so this is purely additive: when activeTab is
// its zero value (TabInstances), every existing instances code path behaves
// exactly as before.
type Tab int

const (
	TabInstances Tab = iota
	TabInstanceGroups
)

// Service implements the services.Service interface for GCE
type Service struct {
	client     *Client
	projectID  string
	table      *components.StandardTable
	groupTable *components.StandardTable

	// Tab (ViewList only — see Tab doc comment)
	activeTab Tab

	// UI Components
	filter        components.FilterModel
	filterSession components.FilterSession[Instance]
	spinner       components.SpinnerModel

	// State
	instances []Instance
	groups    []InstanceGroup
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

	// MIG Update State
	updateGroupForm components.FormModel
	selectedGroup   *InstanceGroup

	// Cache
	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Table Setup
	columns := GetGCEColumns()
	t := components.NewStandardTable(columns)

	groupColumns := []table.Column{
		{Title: "Name", Width: 28},
		{Title: "Location", Width: 15},
		{Title: "Type", Width: 10},
		{Title: "Target Size", Width: 12},
		{Title: "Template", Width: 25},
		{Title: "Autoscaling", Width: 12},
		{Title: "Status", Width: 10},
	}
	gt := components.NewStandardTable(groupColumns)

	svc := &Service{
		table:      t,
		groupTable: gt,
		filter:     components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:    components.NewSpinner(),
		viewState:  ViewList,
		activeTab:  TabInstances,
		cache:      cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredInstances, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Google Compute Engine"
}

func (s *Service) ShortName() string {
	return "gce"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		if s.activeTab == TabInstanceGroups {
			return "[]:Tabs  r:Refresh  u:Update (resize)  d:Delete"
		}
		return "[]:Tabs  r:Refresh  /:Filter  s:Start  x:Stop  R:Reset  z:Suspend  Z:Resume  h:SSH  l:Logs  Ent:Detail  c:Create  u:Update  d:Delete"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  s:Start  x:Stop  R:Reset  z:Suspend  Z:Resume  h:SSH  u:Update  d:Delete"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewUpdateGroup {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	}
	return ""
}

// Focus handles input focus
func (s *Service) Focus() {
	s.table.Focus()
	s.groupTable.Focus()
}

// Blur handles loss of input focus
func (s *Service) Blur() {
	s.table.Blur()
	s.groupTable.Blur()
}

// Msg types
type instancesMsg []Instance
type groupsMsg []InstanceGroup
type errMsg error

// InitService initializes the service logic (API clients)
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

// Init satisfies tea.Model interface
func (s *Service) Init() tea.Cmd {
	return s.tick()
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages specific to GCE
func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		// Forward tick to spinner for animation
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		// Background refresh. Instances always refresh (existing behavior,
		// unchanged); groups only refresh in the background while their tab
		// is actually visible, matching the pattern used elsewhere in this
		// codebase (e.g. cloudrun) for a second, less-frequently-viewed tab.
		if s.activeTab == TabInstanceGroups {
			return s, tea.Batch(s.fetchInstancesCmd(false), s.fetchGroupsCmd(false), s.tick())
		}
		return s, tea.Batch(s.fetchInstancesCmd(false), s.tick())

	// Handle Data Fetching
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

	case groupsMsg:
		s.spinner.Stop()
		s.groups = msg
		s.updateGroupTable(msg)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if msg.err != nil {
			s.err = msg.err
			// Show error toast
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		} else if msg.msg != "" {
			// Show success toast and refresh
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		} else {
			return s, s.Refresh()
		}

	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)
		s.groupTable.HandleWindowSizeDefault(msg)

		// Optional: We could also resize columns here based on width
		// but let's stick to height for now to fix the "truncation" visual

	case tea.MouseMsg:
		// Forward mouse events to the active tab's table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			if s.activeTab == TabInstanceGroups {
				updatedTable, cmd = s.groupTable.Update(msg)
				s.groupTable = updatedTable
			} else {
				updatedTable, cmd = s.table.Update(msg)
				s.table = updatedTable
			}
			return s, cmd
		}

	case tea.KeyMsg:
		// Tab switching (list view only, either tab)
		if s.viewState == ViewList {
			switch msg.String() {
			case "[", "]":
				if s.activeTab == TabInstances {
					s.activeTab = TabInstanceGroups
					if s.groups == nil {
						return s, tea.Batch(s.spinner.Start(""), s.fetchGroupsCmd(false))
					}
				} else {
					s.activeTab = TabInstances
				}
				return s, nil
			}
		}

		// Handle filter mode (only in list view, instances tab — groups has no filter)
		if s.viewState == ViewList && s.activeTab == TabInstances {
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

		// LIST VIEW KEYBINDINGS — Instance Groups tab
		if s.viewState == ViewList && s.activeTab == TabInstanceGroups {
			switch msg.String() {
			case "r":
				return s, s.fetchGroupsCmd(true)
			case "u": // Update (resize)
				if idx := s.groupTable.Cursor(); idx >= 0 && idx < len(s.groups) {
					s.selectedGroup = &s.groups[idx]
					s.updateGroupForm = newGroupUpdateForm(*s.selectedGroup)
					s.viewState = ViewUpdateGroup
					return s, nil
				}
			case "d": // Delete (Confirm)
				if idx := s.groupTable.Cursor(); idx >= 0 && idx < len(s.groups) {
					s.selectedGroup = &s.groups[idx]
					s.pendingAction = "delete-mig"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
					return s, nil
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.groupTable.Update(msg)
			s.groupTable = updatedTable
			return s, cmd
		}

		// LIST VIEW KEYBINDINGS — Instances tab (existing behavior, unchanged)
		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.fetchInstancesCmd(true)
			case "enter": // View Details
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.viewState = ViewDetail
				}
			case "s": // Start (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "start"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "x": // Stop (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "stop"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "R": // Reset (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "reset"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "z": // Suspend (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "suspend"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "Z": // Resume (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "resume"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			case "h": // SSH (Changed from Enter)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					return s, s.SSHCmd(instances[idx])
				}
			case "l": // Logs
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					inst := instances[idx]
					// Filter for GCE instance logs with Strict Quoting
					filter := fmt.Sprintf(`resource.type="gce_instance" AND resource.labels.instance_id="%s"`, inst.ID)
					heading := fmt.Sprintf("VM Instance: %s (ID: %s)", inst.Name, inst.ID)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "gce", Heading: heading} }
				}
			case "c": // Create
				s.createForm = newInstanceCreateForm()
				s.viewState = ViewCreate
				return s, nil
			case "u": // Update
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.updateForm = newInstanceUpdateForm(*s.selectedInstance)
					s.viewState = ViewUpdate
					return s, nil
				}
			case "d": // Delete (Confirm)
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
					s.pendingAction = "delete"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
				}
			}
			// Forward to table
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

		// DETAIL VIEW KEYBINDINGS
		if s.viewState == ViewDetail {
			switch msg.String() {
			case "esc", "q":
				// Return to List View
				s.viewState = ViewList
				s.selectedInstance = nil
				return s, nil
			case "s": // Start (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "start"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "x": // Stop (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "stop"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "R": // Reset (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "reset"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "z": // Suspend (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "suspend"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "Z": // Resume (Confirm)
				if s.selectedInstance != nil {
					s.pendingAction = "resume"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
			case "h": // SSH
				if s.selectedInstance != nil {
					return s, s.SSHCmd(*s.selectedInstance)
				}
			case "u": // Update
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
			}
			// No other updates needed for static detail view
		}

		// CONFIRMATION VIEW KEYBINDINGS
		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter": // Confirm
				var actionCmd tea.Cmd
				switch s.pendingAction {
				case "start":
					actionCmd = s.StartInstanceCmd(*s.selectedInstance)
				case "stop":
					actionCmd = s.StopInstanceCmd(*s.selectedInstance)
				case "reset":
					actionCmd = s.ResetInstanceCmd(*s.selectedInstance)
				case "suspend":
					actionCmd = s.SuspendInstanceCmd(*s.selectedInstance)
				case "resume":
					actionCmd = s.ResumeInstanceCmd(*s.selectedInstance)
				case "delete":
					if s.selectedInstance != nil {
						actionCmd = s.DeleteInstanceCmd(*s.selectedInstance)
					}
				case "delete-mig":
					if s.selectedGroup != nil {
						actionCmd = s.DeleteGroupCmd(*s.selectedGroup)
					}
				}

				// Return to source view. A deleted instance/group no longer
				// exists, so drop back to the list rather than a stale detail view.
				if s.pendingAction == "delete" || s.pendingAction == "delete-mig" {
					s.viewState = ViewList
					s.selectedInstance = nil
					s.selectedGroup = nil
				} else {
					s.viewState = s.actionSource
				}

				// Reset pending state
				s.pendingAction = ""
				return s, actionCmd

			case "n", "esc", "q": // Cancel
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
		}

		// CREATE VIEW KEYBINDINGS
		if s.viewState == ViewCreate {
			result, fcmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				vals := s.createForm.Values()
				s.viewState = ViewList
				return s, s.CreateInstanceCmd(vals["Name"], vals["Zone"], vals["Machine Type"], vals["Source Image"], vals["Network"])
			}
			return s, fcmd
		}

		// UPDATE VIEW KEYBINDINGS
		if s.viewState == ViewUpdate {
			result, fcmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted && s.selectedInstance != nil {
				vals := s.updateForm.Values()
				inst := *s.selectedInstance
				s.viewState = ViewList
				var tags []string
				for _, t := range strings.Split(vals["Tags (comma-separated)"], ",") {
					t = strings.TrimSpace(t)
					if t != "" {
						tags = append(tags, t)
					}
				}
				return s, s.UpdateInstanceTagsCmd(inst, tags)
			}
			return s, fcmd
		}

		// UPDATE GROUP (MIG resize) VIEW KEYBINDINGS
		if s.viewState == ViewUpdateGroup {
			result, fcmd := s.updateGroupForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted && s.selectedGroup != nil {
				size, _ := strconv.ParseInt(s.updateGroupForm.Value("Target Size"), 10, 64)
				group := *s.selectedGroup
				s.viewState = ViewList
				return s, s.ResizeGroupCmd(group, size)
			}
			return s, fcmd
		}

	// Handle Table Events (only in List View)
	default:
		if s.viewState == ViewList {
			if s.activeTab == TabInstanceGroups {
				s.groupTable, cmd = s.groupTable.Update(msg)
			} else {
				s.table, cmd = s.table.Update(msg)
			}
		}
	}

	return s, cmd
}

// View renders the service UI
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

	if s.viewState == ViewUpdateGroup {
		return s.updateGroupForm.View()
	}

	// Default: List View
	return s.renderListView()
}

// Cmd to fetch instances
func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "gce_instances"

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
			// This can happen if Refresh is called before InitService
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

// Cmd to fetch instance groups (MIGs)
func (s *Service) fetchGroupsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "gce_instance_groups"

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if groups, ok := val.([]InstanceGroup); ok {
					return groupsMsg(groups)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		groups, err := s.client.ListInstanceGroups(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, groups, CacheTTL)
		}

		return groupsMsg(groups)
	}
}

// CreateInstanceCmd triggers creation of a new VM instance
func (s *Service) CreateInstanceCmd(name, zone, machineType, sourceImage, network string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateInstance(s.projectID, zone, name, machineType, sourceImage, network); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating instance %s...", name)}
	}
}

// UpdateInstanceTagsCmd triggers a network-tags update for an existing VM instance
func (s *Service) UpdateInstanceTagsCmd(inst Instance, tags []string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateInstanceTags(s.projectID, inst.Zone, inst.Name, tags); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating tags for instance %s...", inst.Name)}
	}
}

// DeleteInstanceCmd triggers deletion of the given VM instance
func (s *Service) DeleteInstanceCmd(inst Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteInstance(s.projectID, inst.Zone, inst.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting instance %s...", inst.Name)}
	}
}

// DeleteGroupCmd triggers deletion of the given Managed Instance Group
func (s *Service) DeleteGroupCmd(group InstanceGroup) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteInstanceGroup(s.projectID, group.Location, group.Name, group.Regional); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting MIG %s...", group.Name)}
	}
}

// ResizeGroupCmd triggers a resize of the given MIG to the given target size
func (s *Service) ResizeGroupCmd(group InstanceGroup, size int64) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ResizeInstanceGroup(s.projectID, group.Location, group.Name, size, group.Regional); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resizing MIG %s to %d instances...", group.Name, size)}
	}
}

// Cmd to refresh (public)
func (s *Service) Refresh() tea.Cmd {
	if s.activeTab == TabInstanceGroups {
		return tea.Batch(
			s.spinner.Start(""),
			s.fetchGroupsCmd(true),
		)
	}
	return tea.Batch(
		s.spinner.Start(""),        // Start animated spinner (empty = use playful messages)
		s.fetchInstancesCmd(false), // Smart refresh
	)
}

// Reset resets the service state
func (s *Service) Reset() {
	s.viewState = ViewList
	s.activeTab = TabInstances
	s.selectedInstance = nil
	s.selectedGroup = nil
	s.err = nil          // Fix: Clear previous errors on reset
	s.table.SetCursor(0) // Optional: reset cursor to top
	s.groupTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

// IsRootView checks if we are in the main list view
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}
