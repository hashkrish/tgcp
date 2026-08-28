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
)

type uptimeChecksMsg []UptimeCheck
type alertPoliciesMsg []AlertPolicy
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

	uptimeTable *components.StandardTable
	alertTable  *components.StandardTable

	activeTab Tab

	uptimeChecks []UptimeCheck
	alertPolicys []AlertPolicy
	spinner      components.SpinnerModel
	err          error

	viewState     ViewState
	selectedCheck *UptimeCheck
	selectedAlert *AlertPolicy

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

	return &Service{
		uptimeTable: uTable,
		alertTable:  aTable,
		spinner:     components.NewSpinner(),
		viewState:   ViewList,
		activeTab:   TabUptimeChecks,
		cache:       cache,
	}
}

func (s *Service) Name() string      { return "Cloud Monitoring" }
func (s *Service) ShortName() string { return "monitoring" }

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		if s.activeTab == TabUptimeChecks {
			return "[]:Switch Tab  Ent:Detail  r:Refresh  n:New Uptime Check"
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
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.activeTab = TabUptimeChecks
	s.selectedCheck = nil
	s.selectedAlert = nil
	s.err = nil
	s.uptimeTable.SetCursor(0)
	s.alertTable.SetCursor(0)
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.uptimeTable.Focus()
	s.alertTable.Focus()
}

func (s *Service) Blur() {
	s.uptimeTable.Blur()
	s.alertTable.Blur()
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
		return s, tea.Batch(s.fetchUptimeChecksCmd(false), s.fetchAlertPoliciesCmd(false), s.tick())

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
			var updatedTable *components.StandardTable
			if s.activeTab == TabUptimeChecks {
				updatedTable, cmd = s.uptimeTable.Update(msg)
				s.uptimeTable = updatedTable
			} else {
				updatedTable, cmd = s.alertTable.Update(msg)
				s.alertTable = updatedTable
			}
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
				return s, s.createUptimeCheckCmd()
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
				if s.activeTab == TabUptimeChecks {
					s.createForm = components.NewForm("New Uptime Check", []components.FormField{
						{Label: "Display Name", Required: true},
						{Label: "Host", Required: true},
						{Label: "Path", Default: "/"},
						{Label: "Check Interval Sec", Default: "60", Required: true},
						{Label: "Protocol", Default: "HTTPS", Required: true},
					})
					s.viewState = ViewCreate
				}
				return s, nil
			case "[", "]", "tab":
				if s.activeTab == TabUptimeChecks {
					s.activeTab = TabAlertPolicies
				} else {
					s.activeTab = TabUptimeChecks
				}
				return s, nil
			case "enter":
				if s.activeTab == TabUptimeChecks {
					if idx := s.uptimeTable.Cursor(); idx >= 0 && idx < len(s.uptimeChecks) {
						s.selectedCheck = &s.uptimeChecks[idx]
						s.viewState = ViewDetail
					}
				} else {
					if idx := s.alertTable.Cursor(); idx >= 0 && idx < len(s.alertPolicys) {
						s.selectedAlert = &s.alertPolicys[idx]
						s.viewState = ViewDetail
					}
				}
				return s, nil
			}

			var updatedTable *components.StandardTable
			if s.activeTab == TabUptimeChecks {
				updatedTable, cmd = s.uptimeTable.Update(msg)
				s.uptimeTable = updatedTable
			} else {
				updatedTable, cmd = s.alertTable.Update(msg)
				s.alertTable = updatedTable
			}
			return s, cmd

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
					if s.activeTab == TabUptimeChecks && s.selectedCheck != nil {
						actionCmd = s.deleteUptimeCheckCmd(*s.selectedCheck)
					} else if s.activeTab == TabAlertPolicies && s.selectedAlert != nil {
						actionCmd = s.deleteAlertPolicyCmd(*s.selectedAlert)
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
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateUptimeCheck(s.projectID, opts); err != nil {
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
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateUptimeCheckPeriod(check.FullName, periodSec); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating uptime check %s...", check.DisplayName)}
	}
}

// deleteUptimeCheckCmd triggers deletion of the given uptime check
func (s *Service) deleteUptimeCheckCmd(check UptimeCheck) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteUptimeCheck(check.FullName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting uptime check %s...", check.DisplayName)}
	}
}

// deleteAlertPolicyCmd triggers deletion of the given alert policy
func (s *Service) deleteAlertPolicyCmd(policy AlertPolicy) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteAlertPolicy(policy.FullName); err != nil {
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
