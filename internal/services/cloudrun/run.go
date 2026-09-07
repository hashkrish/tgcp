package cloudrun

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 30 * time.Second

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

type Tab int

const (
	TabServices Tab = iota
	TabFunctions
)

const (
	ViewList ViewState = iota
	ViewDetail
	ViewConfirmation
	ViewCreate
	ViewUpdate
	ViewRevisions
	ViewRevisionDetail
	ViewTagRevision
	ViewSplitTraffic
	ViewIAM
	ViewIAMForm
)

// newServiceCreateForm builds the FormModel for creating a new Cloud Run service.
func newServiceCreateForm() components.FormModel {
	return components.NewForm("Create Cloud Run Service", []components.FormField{
		{Label: "Name", Placeholder: "my-service", Required: true},
		{Label: "Region", Placeholder: "us-central1", Required: true},
		{Label: "Container Image", Placeholder: "gcr.io/my-project/my-image:latest", Required: true},
		{Label: "Port", Default: "8080", Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 || n > 65535 {
				return "must be a valid port number"
			}
			return ""
		}},
	})
}

// newServiceUpdateForm builds the FormModel for editing a Cloud Run
// service's revision spec and deploying the change as a new revision.
// Fields are seeded from svc's latest-ready revision when available (i.e.
// once the Revisions view has been fetched for this service; otherwise they
// start blank, meaning "leave unchanged" -- see ServiceUpdateOpts). Traffic
// splitting and multi-container/sidecar editing remain out of scope; see
// UpdateServiceCmd/UpdateServiceSpec.
func newServiceUpdateForm(svc RunService) components.FormModel {
	rev := latestReadyRevisionDetail(svc)
	cpu, mem, concurrency, timeout, minScale, maxScale, vpc, sa := "", "", "", "", "", "", "", ""
	if rev != nil {
		cpu, mem = rev.CPULimit, rev.MemoryLimit
		if rev.Concurrency > 0 {
			concurrency = strconv.FormatInt(rev.Concurrency, 10)
		}
		if rev.TimeoutSeconds > 0 {
			timeout = strconv.FormatInt(rev.TimeoutSeconds, 10)
		}
		minScale, maxScale = rev.MinScale, rev.MaxScale
		vpc, sa = rev.VPCConnector, rev.ServiceAccount
	}
	return components.NewForm("Edit & Deploy Revision: "+svc.Name, []components.FormField{
		{Label: "Container Image", Default: svc.Image, Placeholder: "gcr.io/my-project/my-image:latest", Required: true},
		{Label: "CPU Limit", Default: cpu, Placeholder: "1"},
		{Label: "Memory Limit", Default: mem, Placeholder: "512Mi"},
		{Label: "Concurrency", Default: concurrency, Validate: validateOptionalInt},
		{Label: "Timeout Seconds", Default: timeout, Validate: validateOptionalInt},
		{Label: "Min Scale", Default: minScale},
		{Label: "Max Scale", Default: maxScale},
		{Label: "VPC Connector", Default: vpc},
		{Label: "Service Account", Default: sa},
		{Label: "Env Vars", Placeholder: "KEY=value,KEY2=value2 (replaces all plain env vars)"},
	})
}

// latestReadyRevisionDetail finds svc's latest-ready revision entry in
// svc.Revisions, which only carries the full per-revision detail fields
// (resources/concurrency/timeout/scale/VPC connector/service account) once
// the Revisions view has fetched them for this service. Returns nil
// otherwise, in which case the Update form's corresponding fields start
// blank ("leave unchanged").
func latestReadyRevisionDetail(svc RunService) *Revision {
	for i := range svc.Revisions {
		if svc.Revisions[i].Name == svc.LatestReadyRevision {
			return &svc.Revisions[i]
		}
	}
	return nil
}

// validateOptionalInt allows an empty value (meaning "leave unchanged") or
// a valid base-10 integer.
func validateOptionalInt(v string) string {
	if v == "" {
		return ""
	}
	if _, err := strconv.ParseInt(v, 10, 64); err != nil {
		return "must be a whole number"
	}
	return ""
}

// revisionTagPattern matches a valid Knative/Cloud Run traffic tag: lowercase
// alphanumeric, hyphens allowed in the middle, must start with a letter.
var revisionTagPattern = regexp.MustCompile(`^[a-z]([-a-z0-9]*[a-z0-9])?$`)

func validateRevisionTag(v string) string {
	if !revisionTagPattern.MatchString(v) {
		return "must be lowercase alphanumeric/hyphens, starting with a letter"
	}
	return ""
}

// newRevisionTagForm builds the FormModel for assigning a URL tag to a
// revision, matching `gcloud run services update-traffic --update-tags`.
func newRevisionTagForm(rev Revision) components.FormModel {
	return components.NewForm("Tag Revision: "+rev.Name, []components.FormField{
		{Label: "Tag", Default: rev.Tag, Placeholder: "canary", Required: true, Validate: validateRevisionTag},
	})
}

// trafficSplitPattern matches one "revision=percent" pair within a
// comma-separated traffic split spec.
var trafficSplitPattern = regexp.MustCompile(`^\s*([^\s=]+)\s*=\s*(\d{1,3})\s*$`)

// parseTrafficSplit parses a comma-separated "rev1=50,rev2=50" spec into
// TrafficSplitEntry values, validating that percentages are 0-100 and sum
// to exactly 100 (matching gcloud's --to-revisions requirement).
func parseTrafficSplit(v string) ([]TrafficSplitEntry, string) {
	parts := strings.Split(v, ",")
	entries := make([]TrafficSplitEntry, 0, len(parts))
	total := int64(0)
	for _, part := range parts {
		m := trafficSplitPattern.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Sprintf("invalid entry %q, expected revision=percent", strings.TrimSpace(part))
		}
		pct, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil || pct < 0 || pct > 100 {
			return nil, fmt.Sprintf("invalid percent in %q", strings.TrimSpace(part))
		}
		entries = append(entries, TrafficSplitEntry{RevisionName: m[1], Percent: pct})
		total += pct
	}
	if total != 100 {
		return nil, fmt.Sprintf("percentages must sum to 100, got %d", total)
	}
	return entries, ""
}

// newTrafficSplitForm builds the FormModel for an N-way traffic split
// across the service's revisions, matching `gcloud run services
// update-traffic --to-revisions=REV1=P1,REV2=P2,...`.
func newTrafficSplitForm(revisions []Revision) components.FormModel {
	current := make([]string, 0, len(revisions))
	for _, r := range revisions {
		if r.Percent > 0 {
			current = append(current, fmt.Sprintf("%s=%d", r.Name, r.Percent))
		}
	}
	return components.NewForm("Split Traffic", []components.FormField{
		{
			Label:       "Revisions",
			Default:     strings.Join(current, ","),
			Placeholder: "rev1=50,rev2=50",
			Required:    true,
			Validate: func(v string) string {
				_, errMsg := parseTrafficSplit(v)
				return errMsg
			},
		},
	})
}

// servicesMsg is the message used to pass fetched data
type servicesMsg []RunService

// functionsMsg is the message used to pass fetched functions
type functionsMsg []Function

// revisionsMsg carries the full revision history for one service, fetched
// on-demand when entering that service's detail view. service guards
// against applying a stale response after the user has navigated away.
type revisionsMsg struct {
	service   string
	revisions []Revision
}

// errMsg is the standard error message
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g. service creation).
// resource/name/action are filled in by the Cmd that produced it, since
// s.pendingAction is already cleared by the time this message is handled --
// they exist purely for the core.RecordJob call in the Update() handler.
type actionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// revisionActionResultMsg carries the result of a revision-scoped mutating
// action (promote/tag). Unlike actionResultMsg, success re-fetches just the
// selected service's revisions rather than the whole service list.
type revisionActionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// iamPolicyMsg carries the result of a GetServiceIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

// Service implements the services.Service interface
type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable // Services Table
	funcTable *components.StandardTable // Functions Table
	revTable  *components.StandardTable // Revisions Table (per selected service)

	// Tab Component
	activeTab Tab

	// UI Components
	filter                components.FilterModel
	serviceFilterSession  components.FilterSession[RunService]
	functionFilterSession components.FilterSession[Function]
	spinner               components.SpinnerModel

	// State
	services  []RunService
	functions []Function
	err       error

	viewState        ViewState
	selectedService  *RunService
	selectedFunc     *Function
	selectedRevision *Revision

	// Confirmation State
	pendingAction   string              // "delete", "promote", "tag", "untag", "delete-revision", "split-traffic"
	pendingTagValue string              // tag value staged by the tag form, for the "tag" pendingAction
	pendingSplit    []TrafficSplitEntry // traffic split staged by the split form, for the "split-traffic" pendingAction
	actionSource    ViewState           // Where to return after confirmation/cancel

	// Create State
	createForm components.FormModel

	// Update State
	updateForm components.FormModel

	// Tag Revision State
	tagForm components.FormModel

	// Traffic Split State
	splitForm components.FormModel

	// IAM State (Services only): current bindings for the selected service,
	// and the add-binding form. pendingIAMRole/pendingIAMMember are captured
	// at form-submit time and consumed by the "grant" confirmation.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Cache
	cache *core.Cache
}

// NewService creates a new instance of the service
func NewService(cache *core.Cache) *Service {
	// 1. Table Setup
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Status", Width: 10},
		{Title: "Region", Width: 15},
		{Title: "URL", Width: 50},
	}

	t := components.NewStandardTable(columns)

	// 1b. Functions Table Setup
	funcColumns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Region", Width: 15},
		{Title: "State", Width: 10},
		{Title: "Updated", Width: 15},
	}
	ft := components.NewStandardTable(funcColumns)

	// 1c. Revisions Table Setup
	revColumns := []table.Column{
		{Title: "Revision", Width: 30},
		{Title: "Traffic", Width: 16},
		{Title: "Tag", Width: 12},
		{Title: "Created", Width: 12},
	}
	rt := components.NewStandardTable(revColumns)

	svc := &Service{
		table:     t,
		funcTable: ft,
		revTable:  rt,
		activeTab: TabServices,
		filter:    components.NewFilterWithPlaceholder("Filter services..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.serviceFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredServices, svc.updateTable)
	svc.functionFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredFunctions, svc.updateFuncTable)
	return svc
}

// Name returns the full human-readable name
func (s *Service) Name() string {
	return "Cloud Run"
}

// ShortName returns the specialized identifier
func (s *Service) ShortName() string {
	return "run"
}

// HelpText returns context-aware keybindings
func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		if s.activeTab == TabServices {
			return "[]:Tabs  r:Refresh  /:Filter  l:Logs  Ent:Detail  n:Create  u:Edit & Deploy Revision  d:Delete"
		}
		return "[]:Tabs  r:Refresh  /:Filter  l:Logs  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		if s.activeTab == TabServices {
			return "Esc/q:Back  v:Revisions  u:Edit & Deploy Revision  i:IAM  d:Delete"
		}
		return "Esc/q:Back"
	}
	if s.viewState == ViewRevisions {
		return "Esc/q:Back  Ent:Detail  p:Promote  s:Split  t:Tag  T:Untag  d:Delete  r:Refresh  l:Logs"
	}
	if s.viewState == ViewRevisionDetail {
		return "Esc/q:Back  p:Promote  t:Tag  T:Untag  d:Delete"
	}
	if s.viewState == ViewIAM {
		return "Esc/q:Back  a:Grant Role"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewTagRevision || s.viewState == ViewSplitTraffic || s.viewState == ViewIAMForm {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Lifecycle & Interface Implementation
// -----------------------------------------------------------------------------

// InitService initializes the API client
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

// Init startup commands
func (s *Service) Init() tea.Cmd {
	return s.tick()
}

// tick creates a background ticker for cache invalidation
func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Refresh triggers a forced data reload
func (s *Service) Refresh() tea.Cmd {
	var fetchCmd tea.Cmd
	if s.activeTab == TabServices {
		fetchCmd = s.fetchDataCmd(true)
	} else {
		fetchCmd = s.fetchFunctionsCmd(true)
	}
	return tea.Batch(
		s.spinner.Start(""),
		fetchCmd,
	)
}

// Reset clears the service state when navigating away or switching projects
func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedService = nil
	s.selectedRevision = nil
	s.pendingAction = ""
	s.pendingTagValue = ""
	s.pendingSplit = nil
	s.iamBindings = nil
	s.err = nil          // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0) // Reset table position
	s.funcTable.SetCursor(0)
	s.revTable.SetCursor(0)
	s.activeTab = TabServices // Default to Services tab
	s.filter.ExitFilterMode()
}

// SetActiveTab switches to a specific tab by string key, so the command
// palette can deep-link directly into a sub-tab (e.g. "Functions") instead
// of always landing on the default tab (see serviceSubTabs in
// internal/ui/model.go). Returns false for an unrecognized key, treated as
// a harmless no-op by callers. The returned tea.Cmd mirrors what the
// '['/']' key handler already does when switching to Functions -- a forced
// refetch plus re-applying its filter session -- without it the Functions
// table would stay empty/unfiltered until an unrelated refresh touched it.
func (s *Service) SetActiveTab(tab string) (bool, tea.Cmd) {
	switch tab {
	case "services":
		s.activeTab = TabServices
		s.serviceFilterSession.Apply(s.services)
	case "functions":
		s.activeTab = TabFunctions
		s.functionFilterSession.Apply(s.functions)
		return true, tea.Batch(s.fetchFunctionsCmd(true), s.spinner.Start(""))
	default:
		return false, nil
	}
	return true, nil
}

// IsRootView returns true if we are at the top-level list
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// cycleTab toggles between the Services/Functions tabs, shared by the
// direct "[", "]" keys and NextTab/PrevTab.
func (s *Service) cycleTab() (tea.Cmd, bool) {
	s.filter.ExitFilterMode()
	if s.activeTab == TabServices {
		s.activeTab = TabFunctions
		s.functionFilterSession.Apply(s.functions)
		return tea.Batch(s.fetchFunctionsCmd(true), s.spinner.Start("")), true
	}
	s.activeTab = TabServices
	s.serviceFilterSession.Apply(s.services)
	return nil, true
}

// NextTab/PrevTab implement services.TabCycler. There are only two tabs, so
// either direction toggles the same way.
func (s *Service) NextTab() (tea.Cmd, bool) {
	if s.viewState != ViewList || s.filter.IsActive() {
		return nil, false
	}
	return s.cycleTab()
}

func (s *Service) PrevTab() (tea.Cmd, bool) {
	return s.NextTab()
}

// Focus handles input focus (Visual Highlight)
func (s *Service) Focus() {
	s.table.Focus()
	s.funcTable.Focus()
	s.revTable.Focus()
}

// Blur handles loss of input focus (Visual Dimming)
func (s *Service) Blur() {
	s.table.Blur()
	s.funcTable.Blur()
	s.revTable.Blur()
}

// -----------------------------------------------------------------------------
// Update Loop
// -----------------------------------------------------------------------------

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	// Spinner Animation
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	// 1. Background Tick
	case tickMsg:
		var batch []tea.Cmd
		if s.activeTab == TabServices {
			batch = append(batch, s.fetchDataCmd(false))
		} else {
			batch = append(batch, s.fetchFunctionsCmd(false))
		}
		batch = append(batch, s.tick())
		return s, tea.Batch(batch...)

	// 2. Data Loaded
	case servicesMsg:
		s.spinner.Stop()
		s.services = msg
		s.serviceFilterSession.Apply(s.services)
		if s.selectedService != nil {
			for i := range s.services {
				if s.services[i].Name == s.selectedService.Name {
					s.selectedService = &s.services[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case revisionsMsg:
		if s.selectedService != nil && s.selectedService.Name == msg.service {
			s.selectedService.Revisions = msg.revisions
			for i := range s.services {
				if s.services[i].Name == msg.service {
					s.services[i].Revisions = msg.revisions
					break
				}
			}
			if s.viewState == ViewRevisions {
				s.updateRevTable(msg.revisions)
			}
		}
		return s, nil

	case functionsMsg:
		s.spinner.Stop()
		s.functions = msg
		s.functionFilterSession.Apply(s.functions)
		if s.selectedFunc != nil {
			for i := range s.functions {
				if s.functions[i].Name == s.selectedFunc.Name {
					s.selectedFunc = &s.functions[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	// 3. Error Handling
	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case iamPolicyMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.iamBindings = msg.bindings
		s.viewState = ViewIAM
		return s, nil

	case actionResultMsg:
		if msg.resource != "" {
			job := core.Job{
				ProjectID: s.projectID,
				Service:   s.ShortName(),
				Resource:  msg.resource,
				Name:      msg.name,
				Action:    msg.action,
				Status:    core.JobSuccess,
			}
			if msg.err != nil {
				job.Status = core.JobFailed
				job.Error = msg.err.Error()
			}
			core.RecordJob(job)
		}
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		if s.viewState == ViewIAM && s.selectedService != nil {
			// A grant just landed -- re-fetch bindings instead of the
			// service-list refresh below, which wouldn't reflect it.
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.fetchIAMCmd(*s.selectedService),
			)
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

	case revisionActionResultMsg:
		if msg.resource != "" {
			job := core.Job{
				ProjectID: s.projectID,
				Service:   s.ShortName(),
				Resource:  msg.resource,
				Name:      msg.name,
				Action:    msg.action,
				Status:    core.JobSuccess,
			}
			if msg.err != nil {
				job.Status = core.JobFailed
				job.Error = msg.err.Error()
			}
			core.RecordJob(job)
		}
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		var cmds []tea.Cmd
		if msg.msg != "" {
			cmds = append(cmds, func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			})
		}
		if s.selectedService != nil {
			cmds = append(cmds, s.fetchRevisionsCmd(*s.selectedService, true))
		}
		return s, tea.Batch(cmds...)

	// 4. Window Resize
	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)
		s.funcTable.HandleWindowSizeDefault(msg)
		s.revTable.HandleWindowSizeDefault(msg)

	// 4.5 Mouse Input
	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		if s.viewState == ViewList {
			if s.activeTab == TabServices {
				var updatedTable *components.StandardTable
				updatedTable, cmd = s.table.Update(msg)
				s.table = updatedTable
			} else {
				var updatedTable *components.StandardTable
				updatedTable, cmd = s.funcTable.Update(msg)
				s.funcTable = updatedTable
			}
			return s, cmd
		}
		if s.viewState == ViewRevisions {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.revTable.Update(msg)
			s.revTable = updatedTable
			return s, cmd
		}

	// 5. User Input
	case tea.KeyMsg:
		// Handle filter mode (only in list view)
		if s.viewState == ViewList {
			var result components.FilterUpdateResult
			if s.activeTab == TabServices {
				result = s.serviceFilterSession.HandleKey(msg)
			} else {
				result = s.functionFilterSession.HandleKey(msg)
			}

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
			case "[", "]":
				// Direct tab-cycle keys; real Tab/Shift+Tab go through NextTab/PrevTab.
				cmd, _ := s.cycleTab()
				return s, cmd
			case "r":
				return s, s.Refresh()
			case "/":
				// Enter filter mode
				cmd := s.filter.EnterFilterMode()
				return s, cmd
			case "enter":
				// Handle detail view selection
				if s.activeTab == TabServices {
					if s.selectedService == nil {
						svcs := s.getFilteredServices(s.services, s.filter.Value())
						if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
							s.selectedService = &svcs[idx]
							s.viewState = ViewDetail
							return s, s.fetchRevisionsCmd(*s.selectedService, false)
						}
					}
				} else {
					funcs := s.getFilteredFunctions(s.functions, s.filter.Value())
					if idx := s.funcTable.Cursor(); idx >= 0 && idx < len(funcs) {
						s.selectedFunc = &funcs[idx]
						s.viewState = ViewDetail
					}
				}
			case "l": // Logs
				if s.activeTab == TabServices {
					svcs := s.getFilteredServices(s.services, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
						svc := svcs[idx]
						// Strict quoting for filter
						filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.service_name="%s"`, svc.Name)
						heading := fmt.Sprintf("Service: %s", svc.Name)
						return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "run", Heading: heading} }
					}
				} else {
					funcs := s.getFilteredFunctions(s.functions, s.filter.Value())
					if idx := s.funcTable.Cursor(); idx >= 0 && idx < len(funcs) {
						fn := funcs[idx]
						filter := fmt.Sprintf(`resource.type="cloud_function" AND resource.labels.function_name="%s"`, fn.Name)
						heading := fmt.Sprintf("Function: %s", fn.Name)
						return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "run", Heading: heading} }
					}
				}
			case "n": // Create (Services tab only)
				if s.activeTab == TabServices {
					s.createForm = newServiceCreateForm()
					s.viewState = ViewCreate
					return s, nil
				}
			case "u": // Update (Services tab only)
				if s.activeTab == TabServices {
					svcs := s.getFilteredServices(s.services, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
						s.selectedService = &svcs[idx]
						s.updateForm = newServiceUpdateForm(*s.selectedService)
						s.viewState = ViewUpdate
						return s, nil
					}
				}
			case "d": // Delete (Services tab only, Confirm)
				if s.activeTab == TabServices {
					svcs := s.getFilteredServices(s.services, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
						s.selectedService = &svcs[idx]
						s.pendingAction = "delete"
						s.actionSource = ViewList
						s.viewState = ViewConfirmation
						return s, nil
					}
				}
			}
			var updatedTable *components.StandardTable
			if s.activeTab == TabServices {
				updatedTable, cmd = s.table.Update(msg)
				s.table = updatedTable
			} else {
				updatedTable, cmd = s.funcTable.Update(msg)
				s.funcTable = updatedTable
			}
			return s, cmd
		case ViewDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewList
				s.selectedService = nil
				s.selectedFunc = nil
				return s, nil
			case "v":
				if s.activeTab == TabServices && s.selectedService != nil {
					s.updateRevTable(s.selectedService.Revisions)
					s.revTable.SetCursor(0)
					s.viewState = ViewRevisions
				}
				return s, nil
			case "u":
				if s.activeTab == TabServices && s.selectedService != nil {
					s.updateForm = newServiceUpdateForm(*s.selectedService)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "d":
				if s.activeTab == TabServices && s.selectedService != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i":
				if s.activeTab == TabServices && s.selectedService != nil {
					return s, tea.Batch(s.fetchIAMCmd(*s.selectedService), s.spinner.Start(""))
				}
				return s, nil
			}

		case ViewIAM:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "a":
				if s.selectedService != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedService.Name)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}

		case ViewIAMForm:
			result, fcmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.selectedService != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd

		case ViewRevisions:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				s.selectedRevision = nil
				return s, nil
			case "r": // Force-refresh, bypassing the cached revision list
				if s.selectedService != nil {
					return s, s.fetchRevisionsCmd(*s.selectedService, true)
				}
				return s, nil
			case "l": // Logs scoped to the selected revision
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) {
						rev := revs[idx]
						filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.revision_name="%s"`, rev.Name)
						heading := fmt.Sprintf("Revision: %s", rev.Name)
						return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "run", Heading: heading} }
					}
				}
				return s, nil
			case "enter":
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) {
						s.selectedRevision = &revs[idx]
						s.viewState = ViewRevisionDetail
					}
				}
				return s, nil
			case "p": // Promote selected revision to 100% traffic
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) {
						s.selectedRevision = &revs[idx]
						s.pendingAction = "promote"
						s.actionSource = ViewRevisions
						s.viewState = ViewConfirmation
					}
				}
				return s, nil
			case "t": // Tag selected revision
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) {
						s.selectedRevision = &revs[idx]
						s.tagForm = newRevisionTagForm(*s.selectedRevision)
						s.actionSource = ViewRevisions
						s.viewState = ViewTagRevision
					}
				}
				return s, nil
			case "s": // Split traffic across revisions
				if s.selectedService != nil {
					s.splitForm = newTrafficSplitForm(s.selectedService.Revisions)
					s.actionSource = ViewRevisions
					s.viewState = ViewSplitTraffic
				}
				return s, nil
			case "T": // Remove tag from selected revision
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) && revs[idx].Tag != "" {
						s.selectedRevision = &revs[idx]
						s.pendingAction = "untag"
						s.actionSource = ViewRevisions
						s.viewState = ViewConfirmation
					}
				}
				return s, nil
			case "d": // Delete selected revision
				if s.selectedService != nil {
					revs := s.selectedService.Revisions
					if idx := s.revTable.Cursor(); idx >= 0 && idx < len(revs) {
						s.selectedRevision = &revs[idx]
						s.pendingAction = "delete-revision"
						s.actionSource = ViewRevisions
						s.viewState = ViewConfirmation
					}
				}
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.revTable.Update(msg)
			s.revTable = updatedTable
			return s, cmd

		case ViewRevisionDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewRevisions
				s.selectedRevision = nil
				return s, nil
			case "p":
				if s.selectedService != nil && s.selectedRevision != nil {
					s.pendingAction = "promote"
					s.actionSource = ViewRevisionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "t":
				if s.selectedService != nil && s.selectedRevision != nil {
					s.tagForm = newRevisionTagForm(*s.selectedRevision)
					s.actionSource = ViewRevisionDetail
					s.viewState = ViewTagRevision
				}
				return s, nil
			case "T":
				if s.selectedService != nil && s.selectedRevision != nil && s.selectedRevision.Tag != "" {
					s.pendingAction = "untag"
					s.actionSource = ViewRevisionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "l":
				if s.selectedRevision != nil {
					rev := *s.selectedRevision
					filter := fmt.Sprintf(`resource.type="cloud_run_revision" AND resource.labels.revision_name="%s"`, rev.Name)
					heading := fmt.Sprintf("Revision: %s", rev.Name)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "run", Heading: heading} }
				}
				return s, nil
			case "d":
				if s.selectedService != nil && s.selectedRevision != nil {
					s.pendingAction = "delete-revision"
					s.actionSource = ViewRevisionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}

		case ViewTagRevision:
			result, fcmd := s.tagForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.actionSource
				return s, nil
			}
			if result.Submitted {
				vals := s.tagForm.Values()
				s.pendingTagValue = vals["Tag"]
				s.pendingAction = "tag"
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd

		case ViewSplitTraffic:
			result, fcmd := s.splitForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.actionSource
				return s, nil
			}
			if result.Submitted {
				vals := s.splitForm.Values()
				split, errMsg := parseTrafficSplit(vals["Revisions"])
				if errMsg != "" {
					return s, nil
				}
				s.pendingSplit = split
				s.pendingAction = "split-traffic"
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				switch s.pendingAction {
				case "delete":
					if s.selectedService != nil {
						actionCmd = s.DeleteServiceCmd(*s.selectedService)
					}
					s.viewState = ViewList
					s.selectedService = nil
				case "promote":
					if s.selectedService != nil && s.selectedRevision != nil {
						actionCmd = s.PromoteRevisionCmd(*s.selectedService, *s.selectedRevision)
					}
					s.viewState = ViewRevisions
				case "tag":
					if s.selectedService != nil && s.selectedRevision != nil {
						actionCmd = s.TagRevisionCmd(*s.selectedService, *s.selectedRevision, s.pendingTagValue)
					}
					s.viewState = ViewRevisions
				case "untag":
					if s.selectedService != nil && s.selectedRevision != nil {
						actionCmd = s.UntagRevisionCmd(*s.selectedService, *s.selectedRevision)
					}
					s.viewState = s.actionSource
				case "delete-revision":
					if s.selectedService != nil && s.selectedRevision != nil {
						actionCmd = s.DeleteRevisionCmd(*s.selectedService, *s.selectedRevision)
					}
					s.viewState = ViewRevisions
					s.selectedRevision = nil
				case "split-traffic":
					if s.selectedService != nil {
						actionCmd = s.SetTrafficSplitCmd(*s.selectedService, s.pendingSplit)
					}
					s.viewState = ViewRevisions
				case "grant":
					if s.selectedService != nil {
						actionCmd = s.addIAMBindingCmd(*s.selectedService, s.pendingIAMRole, s.pendingIAMMember)
					}
					s.viewState = ViewIAM
				}
				s.pendingAction = ""
				s.pendingTagValue = ""
				s.pendingSplit = nil
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				s.pendingTagValue = ""
				s.pendingSplit = nil
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
				port, _ := strconv.ParseInt(vals["Port"], 10, 64)
				s.viewState = ViewList
				return s, s.CreateServiceCmd(vals["Name"], vals["Region"], vals["Container Image"], port)
			}
			return s, fcmd

		case ViewUpdate:
			result, fcmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted && s.selectedService != nil {
				vals := s.updateForm.Values()
				svc := *s.selectedService
				s.viewState = ViewList
				return s, s.UpdateServiceCmd(svc.Name, svc.Region, ServiceUpdateOpts{
					Image:          vals["Container Image"],
					CPULimit:       vals["CPU Limit"],
					MemoryLimit:    vals["Memory Limit"],
					Concurrency:    vals["Concurrency"],
					TimeoutSeconds: vals["Timeout Seconds"],
					MinScale:       vals["Min Scale"],
					MaxScale:       vals["Max Scale"],
					VPCConnector:   vals["VPC Connector"],
					ServiceAccount: vals["Service Account"],
					EnvVars:        vals["Env Vars"],
				})
			}
			return s, fcmd
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View Rendering
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Services")
	}

	// Show animated spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewRevisions {
		return s.renderRevisionsView()
	}

	if s.viewState == ViewRevisionDetail {
		return s.renderRevisionDetailView()
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

	if s.viewState == ViewTagRevision {
		return s.tagForm.View()
	}

	if s.viewState == ViewSplitTraffic {
		return s.splitForm.View()
	}

	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}

	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}

	// Default: List View
	return s.renderWithTabs()
}

// renderIAMView renders the current IAM policy bindings for the selected
// service, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedService == nil {
		return "Error: No service selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedService.Name, rows)
}

func (s *Service) renderWithTabs() string {
	// Tabs
	var tabs string
	var tableView string
	listLabel := "Services"

	// Filter Bar
	filterBar := s.filter.View() + "\n"

	if s.activeTab == TabServices {
		tabs = lipgloss.JoinHorizontal(lipgloss.Top,
			styles.ActiveTabStyle.Render(" Services "),
			styles.InactiveTabStyle.Render(" Functions "),
		)
		tableView = s.table.View()
	} else {
		listLabel = "Functions"
		tabs = lipgloss.JoinHorizontal(lipgloss.Top,
			styles.InactiveTabStyle.Render(" Services "),
			styles.ActiveTabStyle.Render(" Functions "),
		)
		tableView = s.funcTable.View()
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		listLabel,
	)

	// Status pills or empty state above the table
	var summary string
	if s.activeTab == TabServices {
		if len(s.services) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, filterBar, components.EmptyState("services"))
		}
		states := make([]string, 0, len(s.services))
		for _, svc := range s.services {
			states = append(states, string(svc.Status))
		}
		summary = components.StatusSummary(states) + "\n"
	} else {
		if len(s.functions) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, filterBar, components.EmptyState("functions"))
		}
		states := make([]string, 0, len(s.functions))
		for _, f := range s.functions {
			states = append(states, f.State)
		}
		summary = components.StatusSummary(states) + "\n"
	}

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, filterBar, summary, tableView)
}

func (s *Service) renderDetailView() string {
	if s.activeTab == TabFunctions {
		return s.renderFuncDetailView()
	}

	if s.selectedService == nil {
		return "No service selected"
	}

	svc := s.selectedService

	// Title
	// Title
	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		svc.Name,
	)

	footerHint := "Press 'q' or 'esc' to return"
	if len(svc.Revisions) > 0 {
		footerHint = fmt.Sprintf("Press 'v' to view %d revision(s)  |  q Back", len(svc.Revisions))
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Service Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: svc.Name},
			{Key: "Region", Value: svc.Region},
			{Key: "Status", Value: components.RenderStatus(string(svc.Status))},
			{Key: "URL", Value: svc.URL},
			{Key: "Revision", Value: svc.LatestReadyRevision},
		},
		FooterHint: footerHint,
	})

	return lipgloss.JoinVertical(lipgloss.Left, title, "", card)
}

// renderRevisionsView renders the scrollable list of a service's revisions.
func (s *Service) renderRevisionsView() string {
	if s.selectedService == nil {
		return "No service selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Name,
		"Revisions",
	)

	if len(s.selectedService.Revisions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("revisions"))
	}

	hint := "p Promote  |  s Split Traffic  |  t Tag  |  T Remove Tag  |  d Delete  |  r Refresh  |  l Logs  |  enter Details  |  q Back"
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.revTable.View(), "", styles.HelpStyle.Render(hint))
}

// renderRevisionDetailView renders full details for a single revision.
func (s *Service) renderRevisionDetailView() string {
	if s.selectedService == nil || s.selectedRevision == nil {
		return "No revision selected"
	}
	rev := s.selectedRevision

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Name,
		"Revisions",
		rev.Name,
	)

	created := ""
	if !rev.Created.IsZero() {
		created = rev.Created.Format("2006-01-02 15:04")
	}
	tag := rev.Tag
	if tag == "" {
		tag = "(none)"
	}

	orDash := func(v string) string {
		if v == "" {
			return "-"
		}
		return v
	}
	resources := orDash(rev.CPULimit)
	if rev.MemoryLimit != "" {
		if resources == "-" {
			resources = rev.MemoryLimit
		} else {
			resources = fmt.Sprintf("%s CPU / %s", rev.CPULimit, rev.MemoryLimit)
		}
	}
	concurrency := "-"
	if rev.Concurrency > 0 {
		concurrency = fmt.Sprintf("%d", rev.Concurrency)
	}
	timeout := "-"
	if rev.TimeoutSeconds > 0 {
		timeout = fmt.Sprintf("%ds", rev.TimeoutSeconds)
	}
	scale := fmt.Sprintf("min %s / max %s", orDash(rev.MinScale), orDash(rev.MaxScale))
	env := "(none)"
	if len(rev.EnvVars) > 0 {
		env = strings.Join(rev.EnvVars, ", ")
	}
	conditions := "-"
	if len(rev.Conditions) > 0 {
		conditions = strings.Join(rev.Conditions, ", ")
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Revision Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: rev.Name},
			{Key: "Image", Value: rev.Image},
			{Key: "Image Digest", Value: orDash(rev.ImageDigest)},
			{Key: "Traffic", Value: fmt.Sprintf("%d%%", rev.Percent)},
			{Key: "Latest", Value: fmt.Sprintf("%t", rev.Latest)},
			{Key: "Tag", Value: tag},
			{Key: "Created", Value: created},
			{Key: "Resources", Value: resources},
			{Key: "Concurrency", Value: concurrency},
			{Key: "Timeout", Value: timeout},
			{Key: "Scale", Value: scale},
			{Key: "VPC Connector", Value: orDash(rev.VPCConnector)},
			{Key: "Service Account", Value: orDash(rev.ServiceAccount)},
			{Key: "Env Vars", Value: env},
			{Key: "Volumes", Value: fmt.Sprintf("%d", rev.VolumeCount)},
			{Key: "Conditions", Value: conditions},
		},
		FooterHint: "p Promote  |  t Tag  |  T Remove Tag  |  d Delete  |  l Logs  |  q Back",
	})

	return lipgloss.JoinVertical(lipgloss.Left, title, "", card)
}

// renderConfirmation renders the confirmation dialog for the pending action:
// service deletion/traffic-split, or a revision promote/tag/untag/delete.
func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "promote":
		if s.selectedService == nil || s.selectedRevision == nil {
			return "Error: No revision selected"
		}
		message := fmt.Sprintf(
			"Send 100%% of traffic on %s to revision %s?",
			styles.TitleStyle.Render(s.selectedService.Name),
			styles.TitleStyle.Render(s.selectedRevision.Name),
		)
		return components.RenderConfirmationWithMessage("promote", s.selectedRevision.Name, "revision", message)
	case "tag":
		if s.selectedService == nil || s.selectedRevision == nil {
			return "Error: No revision selected"
		}
		message := fmt.Sprintf(
			"Tag revision %s as %q?\n\nThe existing traffic split is preserved.",
			styles.TitleStyle.Render(s.selectedRevision.Name),
			s.pendingTagValue,
		)
		return components.RenderConfirmationWithMessage("tag", s.selectedRevision.Name, "revision", message)
	case "untag":
		if s.selectedService == nil || s.selectedRevision == nil {
			return "Error: No revision selected"
		}
		message := fmt.Sprintf(
			"Remove tag %q from revision %s?",
			s.selectedRevision.Tag,
			styles.TitleStyle.Render(s.selectedRevision.Name),
		)
		return components.RenderConfirmationWithMessage("untag", s.selectedRevision.Name, "revision", message)
	case "delete-revision":
		if s.selectedService == nil || s.selectedRevision == nil {
			return "Error: No revision selected"
		}
		return components.RenderConfirmation("delete", s.selectedRevision.Name, "revision")
	case "split-traffic":
		if s.selectedService == nil {
			return "Error: No service selected"
		}
		parts := make([]string, 0, len(s.pendingSplit))
		for _, e := range s.pendingSplit {
			parts = append(parts, fmt.Sprintf("%s: %d%%", e.RevisionName, e.Percent))
		}
		message := fmt.Sprintf(
			"Set traffic split on %s?\n\n%s",
			styles.TitleStyle.Render(s.selectedService.Name),
			strings.Join(parts, "\n"),
		)
		return components.RenderConfirmationWithMessage("split-traffic", s.selectedService.Name, "service", message)
	default:
		if s.selectedService == nil {
			return "Error: No service selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedService.Name, "service")
	}
}

func (s *Service) renderFuncDetailView() string {
	if s.selectedFunc == nil {
		return "No function selected"
	}
	f := s.selectedFunc
	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Functions",
		f.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Function Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: f.Name},
			{Key: "Region", Value: f.Region},
			{Key: "State", Value: components.RenderStatus(f.State)},
			{Key: "Environment", Value: f.Environment},
			{Key: "URL", Value: f.URL},
			{Key: "Last Updated", Value: f.LastUpdated.Format("2006-01-02 15:04")},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		"",
		card,
	)
}

// -----------------------------------------------------------------------------
// Helper Commands
// -----------------------------------------------------------------------------

// CreateServiceCmd triggers creation of a new Cloud Run service
func (s *Service) CreateServiceCmd(name, region, image string, port int64) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "service", name: name, action: "create"}
		}
		if err := s.client.CreateService(s.projectID, region, name, image, port); err != nil {
			return actionResultMsg{err: err, resource: "service", name: name, action: "create"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating service %s...", name), resource: "service", name: name, action: "create"}
	}
}

// UpdateServiceCmd fires the UpdateServiceSpec API call for the given
// service, deploying a new revision with the given field changes.
func (s *Service) UpdateServiceCmd(name, region string, opts ServiceUpdateOpts) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "service", name: name, action: "update"}
		}
		if err := s.client.UpdateServiceSpec(s.projectID, region, name, opts); err != nil {
			return actionResultMsg{err: err, resource: "service", name: name, action: "update"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deploying new revision for %s...", name), resource: "service", name: name, action: "update"}
	}
}

// DeleteServiceCmd triggers deletion of the given Cloud Run service
func (s *Service) DeleteServiceCmd(svc RunService) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "service", name: svc.Name, action: "delete"}
		}
		if err := s.client.DeleteService(s.projectID, svc.Region, svc.Name); err != nil {
			return actionResultMsg{err: err, resource: "service", name: svc.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting service %s...", svc.Name), resource: "service", name: svc.Name, action: "delete"}
	}
}

// PromoteRevisionCmd sends 100% of traffic on svc to rev, replacing the
// entire traffic split.
func (s *Service) PromoteRevisionCmd(svc RunService, rev Revision) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revisionActionResultMsg{err: fmt.Errorf("client not initialized"), resource: "revision", name: rev.Name, action: "promote"}
		}
		if err := s.client.PromoteRevision(s.projectID, svc.Region, svc.Name, rev.Name); err != nil {
			return revisionActionResultMsg{err: err, resource: "revision", name: rev.Name, action: "promote"}
		}
		return revisionActionResultMsg{msg: fmt.Sprintf("Promoted revision %s to 100%% traffic", rev.Name), resource: "revision", name: rev.Name, action: "promote"}
	}
}

// TagRevisionCmd assigns tag to rev on svc, preserving every other traffic target.
func (s *Service) TagRevisionCmd(svc RunService, rev Revision, tag string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revisionActionResultMsg{err: fmt.Errorf("client not initialized"), resource: "revision", name: rev.Name, action: "tag"}
		}
		if err := s.client.TagRevision(s.projectID, svc.Region, svc.Name, rev.Name, tag); err != nil {
			return revisionActionResultMsg{err: err, resource: "revision", name: rev.Name, action: "tag"}
		}
		return revisionActionResultMsg{msg: fmt.Sprintf("Tagged revision %s as %q", rev.Name, tag), resource: "revision", name: rev.Name, action: "tag"}
	}
}

// UntagRevisionCmd removes rev's URL tag on svc, preserving every other traffic target.
func (s *Service) UntagRevisionCmd(svc RunService, rev Revision) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revisionActionResultMsg{err: fmt.Errorf("client not initialized"), resource: "revision", name: rev.Name, action: "untag"}
		}
		if err := s.client.UntagRevision(s.projectID, svc.Region, svc.Name, rev.Name); err != nil {
			return revisionActionResultMsg{err: err, resource: "revision", name: rev.Name, action: "untag"}
		}
		return revisionActionResultMsg{msg: fmt.Sprintf("Removed tag from revision %s", rev.Name), resource: "revision", name: rev.Name, action: "untag"}
	}
}

// SetTrafficSplitCmd replaces svc's entire traffic split with an arbitrary
// N-way distribution across named revisions.
func (s *Service) SetTrafficSplitCmd(svc RunService, split []TrafficSplitEntry) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revisionActionResultMsg{err: fmt.Errorf("client not initialized"), resource: "service", name: svc.Name, action: "split-traffic"}
		}
		if err := s.client.SetTrafficSplit(s.projectID, svc.Region, svc.Name, split); err != nil {
			return revisionActionResultMsg{err: err, resource: "service", name: svc.Name, action: "split-traffic"}
		}
		return revisionActionResultMsg{msg: fmt.Sprintf("Updated traffic split for %s", svc.Name), resource: "service", name: svc.Name, action: "split-traffic"}
	}
}

// fetchIAMCmd fetches the current IAM policy for a Cloud Run service.
func (s *Service) fetchIAMCmd(svc RunService) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetServiceIAMPolicy(s.projectID, svc.Region, svc.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given Cloud Run service.
func (s *Service) addIAMBindingCmd(svc RunService, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "service", name: svc.Name, action: "grant"}
		}
		if err := s.client.AddServiceIAMBinding(s.projectID, svc.Region, svc.Name, role, member); err != nil {
			return actionResultMsg{err: err, resource: "service", name: svc.Name, action: "grant"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on service %s", role, member, svc.Name), resource: "service", name: svc.Name, action: "grant"}
	}
}

// DeleteRevisionCmd deletes rev. Cloud Run rejects deletion of a revision
// still receiving traffic, so the request simply surfaces that error.
func (s *Service) DeleteRevisionCmd(svc RunService, rev Revision) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revisionActionResultMsg{err: fmt.Errorf("client not initialized"), resource: "revision", name: rev.Name, action: "delete"}
		}
		if err := s.client.DeleteRevision(s.projectID, svc.Region, rev.Name); err != nil {
			return revisionActionResultMsg{err: err, resource: "revision", name: rev.Name, action: "delete"}
		}
		return revisionActionResultMsg{msg: fmt.Sprintf("Deleting revision %s...", rev.Name), resource: "revision", name: rev.Name, action: "delete"}
	}
}

// fetchRevisionsCmd fetches the full revision history for svc, merging in
// the traffic-split info (Percent/Latest/Tag) already parsed from
// ListServices onto the matching revisions by name. Cached per-service for
// CacheTTL unless force is set (used after a mutating revision action, so
// the view reflects the change immediately instead of a stale cache entry).
func (s *Service) fetchRevisionsCmd(svc RunService, force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("cloudrun_revisions_%s:%s", svc.Name, s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if revs, ok := val.([]Revision); ok {
					return revisionsMsg{service: svc.Name, revisions: revs}
				}
			}
		}
		if s.client == nil {
			return core.ToastMsg{Message: "client not initialized", Type: core.ToastError}
		}
		revs, err := s.client.ListRevisions(s.projectID, svc.Region, svc.Name, svc.Revisions)
		if err != nil {
			return core.ToastMsg{Message: fmt.Sprintf("failed to load revisions: %v", err), Type: core.ToastError}
		}
		if s.cache != nil {
			s.cache.Set(key, revs, CacheTTL)
		}
		return revisionsMsg{service: svc.Name, revisions: revs}
	}
}

func (s *Service) fetchDataCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("cloudrun_services:%s", s.projectID)

		// 1. Check Cache
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if svcs, ok := val.([]RunService); ok {
					return servicesMsg(svcs)
				}
			}
		}

		// 2. API Call
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		svcs, err := s.client.ListServices(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		// 3. Update Cache
		if s.cache != nil {
			s.cache.Set(key, svcs, CacheTTL)
		}

		return servicesMsg(svcs)
	}
}

func (s *Service) updateTable(items []RunService) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		status := string(item.Status)
		if item.Status == StatusReady {
			status = "Ready"
		}
		rows[i] = table.Row{
			item.Name,
			status,
			item.Region,
			item.URL,
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateRevTable(revisions []Revision) {
	rows := make([]table.Row, len(revisions))
	for i, rev := range revisions {
		traffic := fmt.Sprintf("%d%%", rev.Percent)
		if rev.Latest {
			traffic += " (latest)"
		}
		created := ""
		if !rev.Created.IsZero() {
			created = rev.Created.Format("2006-01-02")
		}
		rows[i] = table.Row{
			rev.Name,
			traffic,
			rev.Tag,
			created,
		}
	}
	s.revTable.SetRows(rows)
}

func (s *Service) fetchFunctionsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("cloudrun_functions:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Function); ok {
					return functionsMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListFunctions(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return functionsMsg(items)
	}
}

func (s *Service) updateFuncTable(items []Function) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		state := item.State
		// Plain text state
		rows[i] = table.Row{
			item.Name,
			item.Region,
			state,
			item.LastUpdated.Format("2006-01-02"),
		}
	}
	s.funcTable.SetRows(rows)
}

// getFilteredServices returns filtered services based on the query string
func (s *Service) getFilteredServices(services []RunService, query string) []RunService {
	if query == "" {
		return services
	}
	return components.FilterSlice(services, query, func(svc RunService, q string) bool {
		return components.ContainsMatch(svc.Name, string(svc.Status), svc.Region, svc.URL)(q)
	})
}

// getFilteredFunctions returns filtered functions based on the query string
func (s *Service) getFilteredFunctions(functions []Function, query string) []Function {
	if query == "" {
		return functions
	}
	return components.FilterSlice(functions, query, func(fn Function, q string) bool {
		return components.ContainsMatch(fn.Name, fn.Region, fn.State, fn.URL)(q)
	})
}
