package monitoring

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
)

// newUptimeCheckUpdateForm builds the FormModel for updating an uptime
// check's check interval, seeded with its current value. HTTP/path/host,
// content matchers, and alert-policy fields are out of scope.
func newUptimeCheckUpdateForm(check UptimeCheck) components.FormModel {
	return components.NewForm("Update Uptime Check: "+check.DisplayName, []components.FormField{
		{Label: "Check Interval Sec", Default: periodToSeconds(check.Period), Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

// periodToSeconds converts a Go duration string (e.g. "1m0s") to a plain
// integer-seconds string for form pre-population.
func periodToSeconds(period string) string {
	d, err := time.ParseDuration(period)
	if err != nil {
		return "60"
	}
	return strconv.FormatInt(int64(d.Seconds()), 10)
}

// Tab selects which resource type's list/detail is currently active.
// Both Uptime Checks and Alert Policies are read via the same Cloud
// Monitoring API surface (cloud.google.com/go/monitoring/apiv3/v2), just
// via two different typed clients, so this is implemented as one tabbed
// service (following the net.go Subnets/Firewalls pattern) rather than two
// separate top-level services.
type Tab int

const (
	TabUptimeChecks Tab = iota
	TabAlertPolicies
	TabDashboards
	TabSnoozes
)

// tabOrder is the left-to-right cycle order used by the "[", "]", "tab" keys.
var tabOrder = []Tab{TabUptimeChecks, TabAlertPolicies, TabDashboards, TabSnoozes}

// newAlertPolicyCreateForm builds the FormModel for a single-condition
// metric-threshold alert policy, matching CreateAlertPolicy's minimal scope.
func newAlertPolicyCreateForm() components.FormModel {
	return components.NewForm("New Alert Policy", []components.FormField{
		{Label: "Display Name", Required: true},
		{Label: "Metric Filter", Placeholder: `metric.type="compute.googleapis.com/instance/cpu/utilization"`, Required: true},
		{Label: "Comparison", Default: ">", Required: true, Validate: func(v string) string {
			switch v {
			case ">", ">=", "<", "<=", "==", "!=":
				return ""
			}
			return "must be one of > >= < <= == !="
		}},
		{Label: "Threshold Value", Default: "0.8", Required: true, Validate: func(v string) string {
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return "must be a number"
			}
			return ""
		}},
		{Label: "Duration Sec", Default: "60", Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return "must be a non-negative integer"
			}
			return ""
		}},
	})
}

// newSnoozeCreateForm builds the FormModel for a Snooze that suppresses the
// given alert policy's alerts for a fixed duration starting now.
func newSnoozeCreateForm(policies []AlertPolicy) components.FormModel {
	placeholder := "alert-policy-id"
	if len(policies) > 0 {
		placeholder = policies[0].Name
	}
	return components.NewForm("New Snooze", []components.FormField{
		{Label: "Display Name", Required: true},
		{Label: "Alert Policy ID", Placeholder: placeholder, Required: true},
		{Label: "Duration Minutes", Default: "60", Required: true, Validate: func(v string) string {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

type uptimeChecksMsg []UptimeCheck
type alertPoliciesMsg []AlertPolicy
type dashboardsMsg []Dashboard
type snoozesMsg []Snooze
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g.
// uptime check creation).
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

	uptimeTable    *components.StandardTable
	alertTable     *components.StandardTable
	dashboardTable *components.StandardTable
	snoozeTable    *components.StandardTable

	activeTab Tab

	uptimeChecks []UptimeCheck
	alertPolicys []AlertPolicy
	dashboards   []Dashboard
	snoozes      []Snooze
	spinner      components.SpinnerModel
	err          error

	viewState         ViewState
	selectedCheck     *UptimeCheck
	selectedAlert     *AlertPolicy
	selectedDashboard *Dashboard

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	uCols := []table.Column{
		{Title: "Display Name", Width: 28},
		{Title: "Resource Type", Width: 16},
		{Title: "Check Type", Width: 10},
		{Title: "Period", Width: 8},
		{Title: "Timeout", Width: 8},
	}
	uTable := components.NewStandardTable(uCols)

	aCols := []table.Column{
		{Title: "Display Name", Width: 30},
		{Title: "Enabled", Width: 8},
		{Title: "Conditions", Width: 10},
		{Title: "Combiner", Width: 8},
	}
	aTable := components.NewStandardTable(aCols)

	dCols := []table.Column{
		{Title: "Display Name", Width: 40},
		{Title: "ID", Width: 30},
	}
	dTable := components.NewStandardTable(dCols)

	sCols := []table.Column{
		{Title: "Display Name", Width: 26},
		{Title: "Policies", Width: 10},
		{Title: "Start", Width: 17},
		{Title: "End", Width: 17},
	}
	sTable := components.NewStandardTable(sCols)

	return &Service{
		uptimeTable:    uTable,
		alertTable:     aTable,
		dashboardTable: dTable,
		snoozeTable:    sTable,
		spinner:        components.NewSpinner(),
		viewState:      ViewList,
		activeTab:      TabUptimeChecks,
		cache:          cache,
	}
}

func (s *Service) Name() string      { return "Cloud Monitoring" }
func (s *Service) ShortName() string { return "monitoring" }

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		switch s.activeTab {
		case TabUptimeChecks:
			return "[]:Switch Tab  Ent:Detail  r:Refresh  n:New Uptime Check"
		case TabAlertPolicies:
			return "[]:Switch Tab  Ent:Detail  r:Refresh  n:New Alert Policy"
		case TabDashboards:
			return "[]:Switch Tab  r:Refresh  d:Delete"
		case TabSnoozes:
			return "[]:Switch Tab  r:Refresh  n:New Snooze"
		}
		return "[]:Switch Tab  Ent:Detail  r:Refresh"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewDetail && s.activeTab == TabUptimeChecks {
		return "Esc/q:Back  u:Update  d:Delete"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  d:Delete"
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
	return s.tick()
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchUptimeChecksCmd(true),
		s.fetchAlertPoliciesCmd(true),
		s.fetchDashboardsCmd(true),
		s.fetchSnoozesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.activeTab = TabUptimeChecks
	s.selectedCheck = nil
	s.selectedAlert = nil
	s.selectedDashboard = nil
	s.err = nil
	s.uptimeTable.SetCursor(0)
	s.alertTable.SetCursor(0)
	s.dashboardTable.SetCursor(0)
	s.snoozeTable.SetCursor(0)
}

// SetActiveTab switches to a specific tab by string key, so the command
// palette can deep-link directly into a sub-tab (e.g. "Alert Policies")
// instead of always landing on the default tab (see serviceSubTabs in
// internal/ui/model.go). Returns false for an unrecognized key, treated as
// a harmless no-op by callers.
func (s *Service) SetActiveTab(tab string) (bool, tea.Cmd) {
	switch tab {
	case "uptime-checks":
		s.activeTab = TabUptimeChecks
	case "alert-policies":
		s.activeTab = TabAlertPolicies
	case "dashboards":
		s.activeTab = TabDashboards
	case "snoozes":
		s.activeTab = TabSnoozes
	default:
		return false, nil
	}
	return true, nil
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// NextTab/PrevTab implement services.TabCycler.
func (s *Service) NextTab() (tea.Cmd, bool) {
	if s.viewState != ViewList {
		return nil, false
	}
	s.activeTab = nextTab(s.activeTab)
	return nil, true
}

func (s *Service) PrevTab() (tea.Cmd, bool) {
	if s.viewState != ViewList {
		return nil, false
	}
	s.activeTab = prevTab(s.activeTab)
	return nil, true
}

func (s *Service) Focus() {
	s.uptimeTable.Focus()
	s.alertTable.Focus()
	s.dashboardTable.Focus()
	s.snoozeTable.Focus()
}

func (s *Service) Blur() {
	s.uptimeTable.Blur()
	s.alertTable.Blur()
	s.dashboardTable.Blur()
	s.snoozeTable.Blur()
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
		return s, tea.Batch(s.fetchUptimeChecksCmd(false), s.fetchAlertPoliciesCmd(false), s.fetchDashboardsCmd(false), s.fetchSnoozesCmd(false), s.tick())

	case uptimeChecksMsg:
		s.spinner.Stop()
		s.uptimeChecks = msg
		s.updateUptimeTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case alertPoliciesMsg:
		s.spinner.Stop()
		s.alertPolicys = msg
		s.updateAlertTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case dashboardsMsg:
		s.spinner.Stop()
		s.dashboards = msg
		s.updateDashboardTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case snoozesMsg:
		s.spinner.Stop()
		s.snoozes = msg
		s.updateSnoozeTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

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
			s.selectedCheck = nil
			s.selectedAlert = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
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
		s.uptimeTable.HandleWindowSizeDefault(msg)
		s.alertTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			updatedTable, mcmd := s.activeTable().Update(msg)
			s.setActiveTable(updatedTable)
			return s, mcmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				switch s.activeTab {
				case TabAlertPolicies:
					return s, s.createAlertPolicyCmd()
				case TabSnoozes:
					return s, s.createSnoozeCmd()
				default:
					return s, s.createUptimeCheckCmd()
				}
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedCheck != nil {
				return s, s.updateUptimeCheckCmd(*s.selectedCheck)
			}
			return s, formCmd
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		}

		switch s.viewState {
		case ViewList:
			switch msg.String() {
			case "n":
				switch s.activeTab {
				case TabUptimeChecks:
					s.createForm = components.NewForm("New Uptime Check", []components.FormField{
						{Label: "Display Name", Required: true},
						{Label: "Host", Required: true},
						{Label: "Path", Default: "/"},
						{Label: "Check Interval Sec", Default: "60", Required: true},
						{Label: "Protocol", Default: "HTTPS", Required: true},
					})
					s.viewState = ViewCreate
				case TabAlertPolicies:
					s.createForm = newAlertPolicyCreateForm()
					s.viewState = ViewCreate
				case TabSnoozes:
					s.createForm = newSnoozeCreateForm(s.alertPolicys)
					s.viewState = ViewCreate
				}
				return s, nil
			case "[":
				s.activeTab = prevTab(s.activeTab)
				return s, nil
			case "]":
				s.activeTab = nextTab(s.activeTab)
				return s, nil
			case "d":
				if s.activeTab == TabDashboards {
					if idx := s.dashboardTable.Cursor(); idx >= 0 && idx < len(s.dashboards) {
						s.selectedDashboard = &s.dashboards[idx]
						s.pendingAction = "delete"
						s.actionSource = ViewList
						s.viewState = ViewConfirmation
					}
				}
				return s, nil
			case "enter":
				switch s.activeTab {
				case TabUptimeChecks:
					if idx := s.uptimeTable.Cursor(); idx >= 0 && idx < len(s.uptimeChecks) {
						s.selectedCheck = &s.uptimeChecks[idx]
						s.viewState = ViewDetail
					}
				case TabAlertPolicies:
					if idx := s.alertTable.Cursor(); idx >= 0 && idx < len(s.alertPolicys) {
						s.selectedAlert = &s.alertPolicys[idx]
						s.viewState = ViewDetail
					}
				}
				return s, nil
			}

			updatedTable, tcmd := s.activeTable().Update(msg)
			s.setActiveTable(updatedTable)
			return s, tcmd

		case ViewDetail:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedCheck = nil
				s.selectedAlert = nil
				return s, nil
			case "u":
				if s.activeTab == TabUptimeChecks && s.selectedCheck != nil {
					s.updateForm = newUptimeCheckUpdateForm(*s.selectedCheck)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "d":
				if (s.activeTab == TabUptimeChecks && s.selectedCheck != nil) ||
					(s.activeTab == TabAlertPolicies && s.selectedAlert != nil) {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" {
					switch {
					case s.activeTab == TabUptimeChecks && s.selectedCheck != nil:
						actionCmd = s.deleteUptimeCheckCmd(*s.selectedCheck)
					case s.activeTab == TabAlertPolicies && s.selectedAlert != nil:
						actionCmd = s.deleteAlertPolicyCmd(*s.selectedAlert)
					case s.activeTab == TabDashboards && s.selectedDashboard != nil:
						actionCmd = s.deleteDashboardCmd(*s.selectedDashboard)
					}
				}
				s.viewState = s.actionSource
				s.selectedDashboard = nil
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				s.selectedDashboard = nil
				return s, nil
			}
		}
	}

	return s, nil
}

// nextTab cycles through tabOrder, wrapping around at the end.
func nextTab(t Tab) Tab {
	for i, cur := range tabOrder {
		if cur == t {
			return tabOrder[(i+1)%len(tabOrder)]
		}
	}
	return tabOrder[0]
}

func prevTab(t Tab) Tab {
	for i, cur := range tabOrder {
		if cur == t {
			return tabOrder[(i-1+len(tabOrder))%len(tabOrder)]
		}
	}
	return tabOrder[0]
}

// activeTable returns the StandardTable backing the currently active tab.
func (s *Service) activeTable() *components.StandardTable {
	switch s.activeTab {
	case TabAlertPolicies:
		return s.alertTable
	case TabDashboards:
		return s.dashboardTable
	case TabSnoozes:
		return s.snoozeTable
	default:
		return s.uptimeTable
	}
}

// setActiveTable writes back an updated table to whichever field backs the
// currently active tab, mirroring activeTable's routing.
func (s *Service) setActiveTable(t *components.StandardTable) {
	switch s.activeTab {
	case TabAlertPolicies:
		s.alertTable = t
	case TabDashboards:
		s.dashboardTable = t
	case TabSnoozes:
		s.snoozeTable = t
	default:
		s.uptimeTable = t
	}
}

// createAlertPolicyCmd fires the CreateAlertPolicy API call using the
// current createForm values.
func (s *Service) createAlertPolicyCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := AlertPolicyCreateOpts{
		DisplayName:    v["Display Name"],
		MetricFilter:   v["Metric Filter"],
		Comparison:     v["Comparison"],
		ThresholdValue: v["Threshold Value"],
		DurationSec:    v["Duration Sec"],
	}
	s.viewState = ViewList
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "alert policy",
			Name: opts.DisplayName, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateAlertPolicy(s.projectID, opts)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating alert policy %s...", opts.DisplayName)}
	}
}

// deleteDashboardCmd triggers deletion of the given dashboard.
func (s *Service) deleteDashboardCmd(d Dashboard) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "dashboard",
			Name: d.DisplayName, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteDashboard(d.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting dashboard %s...", d.DisplayName)}
	}
}

// createSnoozeCmd fires the CreateSnooze API call using the current
// createForm values, resolving the entered short alert-policy ID to a full
// resource name against the currently loaded policy list.
func (s *Service) createSnoozeCmd() tea.Cmd {
	v := s.createForm.Values()
	displayName := v["Display Name"]
	policyID := v["Alert Policy ID"]
	duration := v["Duration Minutes"]

	fullName := fmt.Sprintf("projects/%s/alertPolicies/%s", s.projectID, policyID)
	for _, p := range s.alertPolicys {
		if p.Name == policyID {
			fullName = p.FullName
			break
		}
	}

	s.viewState = ViewList
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "snooze",
			Name: displayName, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			opts := SnoozeCreateOpts{DisplayName: displayName, AlertPolicyFullName: fullName, DurationMinutes: duration}
			return s.client.CreateSnooze(s.projectID, opts)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating snooze %s...", displayName)}
	}
}

// createUptimeCheckCmd fires the CreateUptimeCheck API call using the
// current createForm values.
func (s *Service) createUptimeCheckCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := UptimeCheckCreateOpts{
		DisplayName:      v["Display Name"],
		Host:             v["Host"],
		Path:             v["Path"],
		CheckIntervalSec: v["Check Interval Sec"],
		Protocol:         v["Protocol"],
	}
	s.viewState = ViewList
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "uptime check",
			Name: opts.DisplayName, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateUptimeCheck(s.projectID, opts)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating uptime check %s...", opts.DisplayName)}
	}
}

// updateUptimeCheckCmd fires the UpdateUptimeCheckPeriod API call using the
// current update-form value.
func (s *Service) updateUptimeCheckCmd(check UptimeCheck) tea.Cmd {
	periodSec, err := strconv.ParseInt(s.updateForm.Value("Check Interval Sec"), 10, 64)
	if err != nil || periodSec <= 0 {
		periodSec = 60
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "uptime check",
			Name: check.DisplayName, Action: "update",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.UpdateUptimeCheckPeriod(check.FullName, periodSec)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating uptime check %s...", check.DisplayName)}
	}
}

// deleteUptimeCheckCmd triggers deletion of the given uptime check
func (s *Service) deleteUptimeCheckCmd(check UptimeCheck) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "uptime check",
			Name: check.DisplayName, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteUptimeCheck(check.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting uptime check %s...", check.DisplayName)}
	}
}

// deleteAlertPolicyCmd triggers deletion of the given alert policy
func (s *Service) deleteAlertPolicyCmd(policy AlertPolicy) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "alert policy",
			Name: policy.DisplayName, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteAlertPolicy(policy.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting alert policy %s...", policy.DisplayName)}
	}
}

// -----------------------------------------------------------------------------
// Data Fetching
// -----------------------------------------------------------------------------

func (s *Service) fetchUptimeChecksCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("monitoring_uptime:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]UptimeCheck); ok {
					return uptimeChecksMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListUptimeChecks(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return uptimeChecksMsg(items)
	}
}

func (s *Service) fetchAlertPoliciesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("monitoring_alerts:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]AlertPolicy); ok {
					return alertPoliciesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListAlertPolicies(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return alertPoliciesMsg(items)
	}
}

func (s *Service) fetchDashboardsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("monitoring_dashboards:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Dashboard); ok {
					return dashboardsMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListDashboards(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return dashboardsMsg(items)
	}
}

func (s *Service) fetchSnoozesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("monitoring_snoozes:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Snooze); ok {
					return snoozesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListSnoozes(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return snoozesMsg(items)
	}
}

// -----------------------------------------------------------------------------
// Table Updates
// -----------------------------------------------------------------------------

func (s *Service) updateUptimeTable() {
	rows := make([]table.Row, len(s.uptimeChecks))
	for i, c := range s.uptimeChecks {
		rows[i] = table.Row{c.DisplayName, c.ResourceType, c.CheckType, c.Period, c.Timeout}
	}
	s.uptimeTable.SetRows(rows)
}

func (s *Service) updateAlertTable() {
	rows := make([]table.Row, len(s.alertPolicys))
	for i, p := range s.alertPolicys {
		enabled := "false"
		if p.Enabled {
			enabled = "true"
		}
		rows[i] = table.Row{p.DisplayName, enabled, fmt.Sprintf("%d", len(p.Conditions)), p.Combiner}
	}
	s.alertTable.SetRows(rows)
}

func (s *Service) updateDashboardTable() {
	rows := make([]table.Row, len(s.dashboards))
	for i, d := range s.dashboards {
		rows[i] = table.Row{d.DisplayName, d.Name}
	}
	s.dashboardTable.SetRows(rows)
}

func (s *Service) updateSnoozeTable() {
	rows := make([]table.Row, len(s.snoozes))
	for i, sn := range s.snoozes {
		start, end := "", ""
		if !sn.StartTime.IsZero() {
			start = sn.StartTime.Format("2006-01-02 15:04")
		}
		if !sn.EndTime.IsZero() {
			end = sn.EndTime.Format("2006-01-02 15:04")
		}
		rows[i] = table.Row{sn.DisplayName, fmt.Sprintf("%d", len(sn.Policies)), start, end}
	}
	s.snoozeTable.SetRows(rows)
}
