// Package loadbalancing (this file) implements the Service UI for Cloud
// Load Balancing.
//
// Scope: Backend Services, Health Checks, URL Maps, Forwarding Rules, and
// SSL Certificates — the core resources involved in understanding how a
// load balancer routes and serves traffic. Backend buckets and target
// proxies are still left out; they're thin pass-through resources with
// little standalone value beyond what URL Maps/Forwarding Rules already
// show.
//
// All resources here live in the same `compute/v1` API surface already used
// by internal/services/gce and internal/services/net, so this package
// reuses that same compute.Service client construction pattern. Every
// resource type here can be either global or regional, so every listing
// uses AggregatedList (project-wide across all scopes) rather than a
// hardcoded region list or per-region fan-out.
//
// No mutating calls are made anywhere in this package — list only.
package loadbalancing

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
	"github.com/yogirk/tgcp/internal/styles"
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
	ViewConfirmation
)

type Tab int

const (
	TabBackendServices Tab = iota
	TabHealthChecks
	TabUrlMaps
	TabForwardingRules
	TabSslCertificates
	tabCount // sentinel, keep last
)

func (t Tab) label() string {
	switch t {
	case TabBackendServices:
		return "Backend Services"
	case TabHealthChecks:
		return "Health Checks"
	case TabUrlMaps:
		return "URL Maps"
	case TabForwardingRules:
		return "Forwarding Rules"
	case TabSslCertificates:
		return "SSL Certificates"
	default:
		return ""
	}
}

type backendServicesMsg []BackendService
type healthChecksMsg []HealthCheck
type urlMapsMsg []UrlMap
type forwardingRulesMsg []ForwardingRule
type sslCertificatesMsg []SslCertificate
type errMsg error

// actionResultMsg carries the result of an async action (e.g. health check
// creation, or a read like get-health). resource/name/action identify what
// was acted on for job-history recording; left empty for read-only actions
// (e.g. get-health), which are never recorded.
type actionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	// Tables
	backendTable *components.StandardTable
	healthTable  *components.StandardTable
	urlMapTable  *components.StandardTable
	fwdRuleTable *components.StandardTable
	sslCertTable *components.StandardTable

	activeTab Tab

	// State
	backendServices []BackendService
	healthChecks    []HealthCheck
	urlMaps         []UrlMap
	forwardingRules []ForwardingRule
	sslCertificates []SslCertificate
	spinner         components.SpinnerModel
	err             error

	viewState       ViewState
	selectedBackend *BackendService
	selectedHealth  *HealthCheck
	selectedUrlMap  *UrlMap
	selectedFwdRule *ForwardingRule
	selectedSslCert *SslCertificate

	createForm     components.FormModel
	createReturnTo ViewState

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	bCols := []table.Column{
		{Title: "Name", Width: 28},
		{Title: "Region", Width: 14},
		{Title: "Protocol", Width: 10},
		{Title: "Scheme", Width: 22},
		{Title: "Health Check", Width: 20},
		{Title: "Backends", Width: 10},
	}
	bTable := components.NewStandardTable(bCols)

	hCols := []table.Column{
		{Title: "Name", Width: 28},
		{Title: "Region", Width: 14},
		{Title: "Type", Width: 8},
		{Title: "Port", Width: 8},
		{Title: "Interval(s)", Width: 12},
		{Title: "Healthy/Unhealthy", Width: 18},
	}
	hTable := components.NewStandardTable(hCols)

	uCols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Region", Width: 14},
		{Title: "Default Service", Width: 30},
	}
	uTable := components.NewStandardTable(uCols)

	fCols := []table.Column{
		{Title: "Name", Width: 26},
		{Title: "Region", Width: 12},
		{Title: "IP Address", Width: 16},
		{Title: "Protocol", Width: 10},
		{Title: "Ports", Width: 12},
		{Title: "Target", Width: 26},
	}
	fTable := components.NewStandardTable(fCols)

	sCols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Region", Width: 12},
		{Title: "Type", Width: 14},
		{Title: "Domains", Width: 34},
	}
	sTable := components.NewStandardTable(sCols)

	return &Service{
		backendTable: bTable,
		healthTable:  hTable,
		urlMapTable:  uTable,
		fwdRuleTable: fTable,
		sslCertTable: sTable,
		spinner:      components.NewSpinner(),
		viewState:    ViewList,
		activeTab:    TabBackendServices,
		cache:        cache,
	}
}

func (s *Service) Name() string      { return "Load Balancing" }
func (s *Service) ShortName() string { return "loadbalancing" }

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		if s.activeTab == TabHealthChecks {
			return "[]:Tabs  r:Refresh  Ent:Detail  n:New Health Check"
		}
		return "[]:Tabs  r:Refresh  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		switch s.activeTab {
		case TabBackendServices:
			return "Esc/q:Back  d:Delete  u:Update Timeout  h:Health  g:Grant IAM"
		case TabUrlMaps:
			return "Esc/q:Back  d:Delete  i:Invalidate Cache"
		default:
			return "Esc/q:Back  d:Delete"
		}
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewCreate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
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

func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchBackendServicesCmd(false),
		s.tick(),
	)
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// fetchCmdForTab returns the fetch command for the given tab.
func (s *Service) fetchCmdForTab(tab Tab, force bool) tea.Cmd {
	switch tab {
	case TabBackendServices:
		return s.fetchBackendServicesCmd(force)
	case TabHealthChecks:
		return s.fetchHealthChecksCmd(force)
	case TabUrlMaps:
		return s.fetchUrlMapsCmd(force)
	case TabForwardingRules:
		return s.fetchForwardingRulesCmd(force)
	case TabSslCertificates:
		return s.fetchSslCertificatesCmd(force)
	default:
		return nil
	}
}

// tabHasData reports whether the given tab's data slice has already been
// fetched at least once (nil means "never fetched").
func (s *Service) tabHasData(tab Tab) bool {
	switch tab {
	case TabBackendServices:
		return s.backendServices != nil
	case TabHealthChecks:
		return s.healthChecks != nil
	case TabUrlMaps:
		return s.urlMaps != nil
	case TabForwardingRules:
		return s.forwardingRules != nil
	case TabSslCertificates:
		return s.sslCertificates != nil
	default:
		return true
	}
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchCmdForTab(s.activeTab, true))
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedBackend = nil
	s.selectedHealth = nil
	s.selectedUrlMap = nil
	s.selectedFwdRule = nil
	s.selectedSslCert = nil
	s.err = nil
	s.backendTable.SetCursor(0)
	s.healthTable.SetCursor(0)
	s.urlMapTable.SetCursor(0)
	s.fwdRuleTable.SetCursor(0)
	s.sslCertTable.SetCursor(0)
	s.activeTab = TabBackendServices
}

// SetActiveTab switches to a specific tab by string key, so the command
// palette can deep-link directly into a sub-tab (e.g. "Health Checks")
// instead of always landing on the default tab (see serviceSubTabs in
// internal/ui/model.go). Returns false for an unrecognized key, treated as
// a harmless no-op by callers. The returned tea.Cmd mirrors what the
// '['/']' key handler already does when switching tabs manually -- fetch
// that tab's data if it hasn't been loaded yet -- without it this tab's
// table would stay empty until an unrelated refresh happened to touch it.
func (s *Service) SetActiveTab(tab string) (bool, tea.Cmd) {
	var t Tab
	switch tab {
	case "backend-services":
		t = TabBackendServices
	case "health-checks":
		t = TabHealthChecks
	case "url-maps":
		t = TabUrlMaps
	case "forwarding-rules":
		t = TabForwardingRules
	case "ssl-certificates":
		t = TabSslCertificates
	default:
		return false, nil
	}
	s.activeTab = t
	if !s.tabHasData(t) {
		return true, tea.Batch(s.spinner.Start(""), s.fetchCmdForTab(t, false))
	}
	return true, nil
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// nextTab/prevTab cycle s.activeTab, fetching that tab's data if not
// already loaded. Shared by the direct "[", "]" keys and NextTab/PrevTab.
func (s *Service) nextTab() (tea.Cmd, bool) {
	s.activeTab = Tab((int(s.activeTab) + 1) % int(tabCount))
	if !s.tabHasData(s.activeTab) {
		return tea.Batch(s.fetchCmdForTab(s.activeTab, false), s.spinner.Start("")), true
	}
	return nil, true
}

func (s *Service) prevTab() (tea.Cmd, bool) {
	s.activeTab = Tab((int(s.activeTab) - 1 + int(tabCount)) % int(tabCount))
	if !s.tabHasData(s.activeTab) {
		return tea.Batch(s.fetchCmdForTab(s.activeTab, false), s.spinner.Start("")), true
	}
	return nil, true
}

// NextTab/PrevTab implement services.TabCycler.
func (s *Service) NextTab() (tea.Cmd, bool) {
	if s.viewState != ViewList {
		return nil, false
	}
	return s.nextTab()
}

func (s *Service) PrevTab() (tea.Cmd, bool) {
	if s.viewState != ViewList {
		return nil, false
	}
	return s.prevTab()
}

func (s *Service) Focus() {
	s.backendTable.Focus()
	s.healthTable.Focus()
	s.urlMapTable.Focus()
	s.fwdRuleTable.Focus()
	s.sslCertTable.Focus()
}

func (s *Service) Blur() {
	s.backendTable.Blur()
	s.healthTable.Blur()
	s.urlMapTable.Blur()
	s.fwdRuleTable.Blur()
	s.sslCertTable.Blur()
}

// activeTable returns the StandardTable backing the currently active tab.
func (s *Service) activeTable() *components.StandardTable {
	switch s.activeTab {
	case TabBackendServices:
		return s.backendTable
	case TabHealthChecks:
		return s.healthTable
	case TabUrlMaps:
		return s.urlMapTable
	case TabForwardingRules:
		return s.fwdRuleTable
	case TabSslCertificates:
		return s.sslCertTable
	default:
		return s.backendTable
	}
}

func (s *Service) setActiveTable(t *components.StandardTable) {
	switch s.activeTab {
	case TabBackendServices:
		s.backendTable = t
	case TabHealthChecks:
		s.healthTable = t
	case TabUrlMaps:
		s.urlMapTable = t
	case TabForwardingRules:
		s.fwdRuleTable = t
	case TabSslCertificates:
		s.sslCertTable = t
	}
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
		return s, tea.Batch(s.fetchCmdForTab(s.activeTab, false), s.tick())

	case backendServicesMsg:
		s.spinner.Stop()
		s.backendServices = msg
		s.updateBackendTable()
		if s.selectedBackend != nil {
			for i := range s.backendServices {
				if s.backendServices[i].Name == s.selectedBackend.Name && s.backendServices[i].Region == s.selectedBackend.Region {
					s.selectedBackend = &s.backendServices[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case healthChecksMsg:
		s.spinner.Stop()
		s.healthChecks = msg
		s.updateHealthTable()
		if s.selectedHealth != nil {
			for i := range s.healthChecks {
				if s.healthChecks[i].Name == s.selectedHealth.Name && s.healthChecks[i].Region == s.selectedHealth.Region {
					s.selectedHealth = &s.healthChecks[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case urlMapsMsg:
		s.spinner.Stop()
		s.urlMaps = msg
		s.updateUrlMapTable()
		if s.selectedUrlMap != nil {
			for i := range s.urlMaps {
				if s.urlMaps[i].Name == s.selectedUrlMap.Name && s.urlMaps[i].Region == s.selectedUrlMap.Region {
					s.selectedUrlMap = &s.urlMaps[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case forwardingRulesMsg:
		s.spinner.Stop()
		s.forwardingRules = msg
		s.updateFwdRuleTable()
		if s.selectedFwdRule != nil {
			for i := range s.forwardingRules {
				if s.forwardingRules[i].Name == s.selectedFwdRule.Name && s.forwardingRules[i].Region == s.selectedFwdRule.Region {
					s.selectedFwdRule = &s.forwardingRules[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case sslCertificatesMsg:
		s.spinner.Stop()
		s.sslCertificates = msg
		s.updateSslCertTable()
		if s.selectedSslCert != nil {
			for i := range s.sslCertificates {
				if s.sslCertificates[i].Name == s.selectedSslCert.Name && s.sslCertificates[i].Region == s.selectedSslCert.Region {
					s.selectedSslCert = &s.sslCertificates[i]
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
		if msg.resource != "" {
			if msg.err != nil {
				core.RecordJob(core.Job{
					ProjectID: s.projectID, Service: s.ShortName(),
					Resource: msg.resource, Name: msg.name, Action: msg.action,
					Status: core.JobFailed, Error: msg.err.Error(),
				})
			} else {
				core.RecordJob(core.Job{
					ProjectID: s.projectID, Service: s.ShortName(),
					Resource: msg.resource, Name: msg.name, Action: msg.action,
					Status: core.JobSuccess,
				})
			}
		}
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			s.selectedBackend = nil
			s.selectedHealth = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
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
		s.backendTable.HandleWindowSizeDefault(msg)
		s.healthTable.HandleWindowSizeDefault(msg)
		s.urlMapTable.HandleWindowSizeDefault(msg)
		s.fwdRuleTable.HandleWindowSizeDefault(msg)
		s.sslCertTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.activeTable().Update(msg)
			s.setActiveTable(updatedTable)
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.createReturnTo
				s.pendingAction = ""
				return s, nil
			}
			if result.Submitted {
				action := s.pendingAction
				s.pendingAction = ""
				switch action {
				case "invalidate-cache":
					if s.selectedUrlMap != nil {
						return s, s.invalidateCacheCmd(*s.selectedUrlMap)
					}
				case "grant-iam":
					if s.selectedBackend != nil {
						return s, s.grantBackendIAMCmd(*s.selectedBackend)
					}
				case "update-timeout":
					if s.selectedBackend != nil {
						return s, s.updateBackendTimeoutCmd(*s.selectedBackend)
					}
				}
				return s, s.createHealthCheckCmd()
			}
			return s, formCmd
		}

		switch s.viewState {
		case ViewList:
			switch msg.String() {
			case "n":
				if s.activeTab == TabHealthChecks {
					s.createForm = components.NewForm("New Health Check", []components.FormField{
						{Label: "Name", Required: true},
						{Label: "Protocol", Default: "HTTP", Required: true},
						{Label: "Port", Default: "80", Required: true},
						{Label: "Check Interval Sec", Default: "5", Required: true},
					})
					s.createReturnTo = ViewList
					s.viewState = ViewCreate
				}
				return s, nil
			case "[":
				cmd, _ := s.prevTab()
				return s, cmd
			case "]":
				cmd, _ := s.nextTab()
				return s, cmd
			case "r":
				return s, s.Refresh()
			case "enter":
				idx := s.activeTable().Cursor()
				switch s.activeTab {
				case TabBackendServices:
					if idx >= 0 && idx < len(s.backendServices) {
						s.selectedBackend = &s.backendServices[idx]
						s.viewState = ViewDetail
					}
				case TabHealthChecks:
					if idx >= 0 && idx < len(s.healthChecks) {
						s.selectedHealth = &s.healthChecks[idx]
						s.viewState = ViewDetail
					}
				case TabUrlMaps:
					if idx >= 0 && idx < len(s.urlMaps) {
						s.selectedUrlMap = &s.urlMaps[idx]
						s.viewState = ViewDetail
					}
				case TabForwardingRules:
					if idx >= 0 && idx < len(s.forwardingRules) {
						s.selectedFwdRule = &s.forwardingRules[idx]
						s.viewState = ViewDetail
					}
				case TabSslCertificates:
					if idx >= 0 && idx < len(s.sslCertificates) {
						s.selectedSslCert = &s.sslCertificates[idx]
						s.viewState = ViewDetail
					}
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.activeTable().Update(msg)
			s.setActiveTable(updatedTable)
			return s, cmd
		case ViewDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewList
				s.selectedBackend = nil
				s.selectedHealth = nil
				s.selectedUrlMap = nil
				s.selectedFwdRule = nil
				s.selectedSslCert = nil
				return s, nil
			case "d":
				if (s.activeTab == TabBackendServices && s.selectedBackend != nil) ||
					(s.activeTab == TabHealthChecks && s.selectedHealth != nil) ||
					(s.activeTab == TabUrlMaps && s.selectedUrlMap != nil) ||
					(s.activeTab == TabForwardingRules && s.selectedFwdRule != nil) ||
					(s.activeTab == TabSslCertificates && s.selectedSslCert != nil) {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "u": // Update timeout (Backend Services tab only)
				if s.activeTab == TabBackendServices && s.selectedBackend != nil {
					s.createForm = components.NewForm("Update Timeout: "+s.selectedBackend.Name, []components.FormField{
						{Label: "Timeout Sec", Default: fmt.Sprintf("%d", s.selectedBackend.TimeoutSec), Required: true, Validate: func(v string) string {
							if _, err := strconv.ParseInt(v, 10, 64); err != nil {
								return "must be an integer"
							}
							return ""
						}},
					})
					s.createReturnTo = ViewDetail
					s.viewState = ViewCreate
					s.pendingAction = "update-timeout"
				}
				return s, nil
			case "h": // Get health (Backend Services tab only)
				if s.activeTab == TabBackendServices && s.selectedBackend != nil {
					return s, s.getBackendHealthCmd(*s.selectedBackend)
				}
				return s, nil
			case "i": // Invalidate CDN cache (URL Maps tab only)
				if s.activeTab == TabUrlMaps && s.selectedUrlMap != nil {
					s.createForm = components.NewForm("Invalidate CDN Cache: "+s.selectedUrlMap.Name, []components.FormField{
						{Label: "Path", Default: "/*", Required: true},
					})
					s.createReturnTo = ViewDetail
					s.viewState = ViewCreate
					s.pendingAction = "invalidate-cache"
				}
				return s, nil
			case "g": // Grant IAM role (Backend Services tab only)
				if s.activeTab == TabBackendServices && s.selectedBackend != nil {
					s.createForm = components.NewForm("Grant IAM Role: "+s.selectedBackend.Name, []components.FormField{
						{Label: "Member", Placeholder: "user:alice@example.com", Required: true},
						{Label: "Role", Default: "roles/compute.loadBalancerServiceUser", Required: true},
					})
					s.createReturnTo = ViewDetail
					s.viewState = ViewCreate
					s.pendingAction = "grant-iam"
				}
				return s, nil
			}

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" {
					switch {
					case s.activeTab == TabBackendServices && s.selectedBackend != nil:
						actionCmd = s.deleteBackendCmd(*s.selectedBackend)
					case s.activeTab == TabHealthChecks && s.selectedHealth != nil:
						actionCmd = s.deleteHealthCheckCmd(*s.selectedHealth)
					case s.activeTab == TabUrlMaps && s.selectedUrlMap != nil:
						actionCmd = s.deleteUrlMapCmd(*s.selectedUrlMap)
					case s.activeTab == TabForwardingRules && s.selectedFwdRule != nil:
						actionCmd = s.deleteForwardingRuleCmd(*s.selectedFwdRule)
					case s.activeTab == TabSslCertificates && s.selectedSslCert != nil:
						actionCmd = s.deleteSslCertificateCmd(*s.selectedSslCert)
					}
				}
				// Stay on actionSource (and keep pendingAction "delete")
				// until actionResultMsg arrives.
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
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Load Balancing")
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

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	return s.renderWithTabs()
}

// renderConfirmation renders the resource-delete confirmation dialog for
// whichever tab/resource is currently selected.
func (s *Service) renderConfirmation() string {
	switch s.activeTab {
	case TabBackendServices:
		if s.selectedBackend == nil {
			return "Error: No backend service selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedBackend.Name, "backend service")
	case TabHealthChecks:
		if s.selectedHealth == nil {
			return "Error: No health check selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedHealth.Name, "health check")
	case TabUrlMaps:
		if s.selectedUrlMap == nil {
			return "Error: No URL map selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedUrlMap.Name, "URL map")
	case TabForwardingRules:
		if s.selectedFwdRule == nil {
			return "Error: No forwarding rule selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedFwdRule.Name, "forwarding rule")
	case TabSslCertificates:
		if s.selectedSslCert == nil {
			return "Error: No SSL certificate selected"
		}
		return components.RenderConfirmation(s.pendingAction, s.selectedSslCert.Name, "SSL certificate")
	}
	return "Error: No resource selected"
}

// createHealthCheckCmd fires the CreateHealthCheck API call using the
// current createForm values.
func (s *Service) createHealthCheckCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := HealthCheckCreateOpts{
		Name:             v["Name"],
		Protocol:         v["Protocol"],
		Port:             v["Port"],
		CheckIntervalSec: v["Check Interval Sec"],
	}
	s.viewState = s.createReturnTo
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateHealthCheck(s.projectID, opts); err != nil {
			return actionResultMsg{err: err, resource: "health check", name: opts.Name, action: "create"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating health check %s...", opts.Name), resource: "health check", name: opts.Name, action: "create"}
	}
}

// invalidateCacheCmd fires the InvalidateUrlMapCache API call using the
// current createForm value.
func (s *Service) invalidateCacheCmd(um UrlMap) tea.Cmd {
	path := s.createForm.Value("Path")
	s.viewState = s.createReturnTo
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.InvalidateUrlMapCache(s.projectID, um.Name, path); err != nil {
			return actionResultMsg{err: err, resource: "URL map", name: um.Name, action: "invalidate-cache"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Invalidating cache for %s at path %s...", um.Name, path), resource: "URL map", name: um.Name, action: "invalidate-cache"}
	}
}

// grantBackendIAMCmd fires the AddBackendServiceIAMBinding API call using
// the current createForm values.
func (s *Service) grantBackendIAMCmd(bs BackendService) tea.Cmd {
	member := s.createForm.Value("Member")
	role := s.createForm.Value("Role")
	s.viewState = s.createReturnTo
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddBackendServiceIAMBinding(s.projectID, bs.Region, bs.Name, role, member); err != nil {
			return actionResultMsg{err: err, resource: "backend service", name: bs.Name, action: "grant-iam"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on backend service %s", role, member, bs.Name), resource: "backend service", name: bs.Name, action: "grant-iam"}
	}
}

// updateBackendTimeoutCmd fires the UpdateBackendServiceTimeout API call
// using the current createForm value.
func (s *Service) updateBackendTimeoutCmd(bs BackendService) tea.Cmd {
	timeout, _ := strconv.ParseInt(s.createForm.Value("Timeout Sec"), 10, 64)
	s.viewState = s.createReturnTo
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateBackendServiceTimeout(s.projectID, bs.Region, bs.Name, timeout); err != nil {
			return actionResultMsg{err: err, resource: "backend service", name: bs.Name, action: "update-timeout"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating timeout for %s...", bs.Name), resource: "backend service", name: bs.Name, action: "update-timeout"}
	}
}

// getBackendHealthCmd fires the GetBackendServiceHealth API call and
// surfaces the result as a toast (there's no dedicated health-detail view).
func (s *Service) getBackendHealthCmd(bs BackendService) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		status, err := s.client.GetBackendServiceHealth(s.projectID, bs.Region, bs.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("%s health: %s", bs.Name, status)}
	}
}

func (s *Service) renderWithTabs() string {
	segments := make([]string, 0, int(tabCount))
	for t := Tab(0); t < tabCount; t++ {
		label := " " + t.label() + " "
		if t == s.activeTab {
			segments = append(segments, styles.ActiveTabStyle.Render(label))
		} else {
			segments = append(segments, styles.InactiveTabStyle.Render(label))
		}
	}
	tabs := lipgloss.JoinHorizontal(lipgloss.Top, segments...)

	var tableView string
	switch s.activeTab {
	case TabBackendServices:
		if len(s.backendServices) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.backendTable.View()
		}
	case TabHealthChecks:
		if len(s.healthChecks) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.healthTable.View()
		}
	case TabUrlMaps:
		if len(s.urlMaps) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.urlMapTable.View()
		}
	case TabForwardingRules:
		if len(s.forwardingRules) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.fwdRuleTable.View()
		}
	case TabSslCertificates:
		if len(s.sslCertificates) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.sslCertTable.View()
		}
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		s.activeTab.label(),
	)

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, tableView)
}

func (s *Service) renderDetailView() string {
	switch s.activeTab {
	case TabHealthChecks:
		return s.renderHealthDetailView()
	case TabUrlMaps:
		return s.renderUrlMapDetailView()
	case TabForwardingRules:
		return s.renderForwardingRuleDetailView()
	case TabSslCertificates:
		return s.renderSslCertDetailView()
	default:
		return s.renderBackendDetailView()
	}
}

func (s *Service) renderBackendDetailView() string {
	if s.selectedBackend == nil {
		return "No backend service selected"
	}
	bs := s.selectedBackend

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Backend Services",
		bs.Name,
	)

	healthCheck := bs.HealthCheckName
	if healthCheck == "" {
		healthCheck = "-"
	}

	rows := []components.KeyValue{
		{Key: "Name", Value: bs.Name},
		{Key: "Region", Value: bs.Region},
		{Key: "Protocol", Value: bs.Protocol},
		{Key: "Load Balancing Scheme", Value: bs.LoadBalancingScheme},
		{Key: "Health Check", Value: healthCheck},
		{Key: "Backend Count", Value: fmt.Sprintf("%d", bs.BackendCount)},
	}
	if bs.Description != "" {
		rows = append(rows, components.KeyValue{Key: "Description", Value: bs.Description})
	}
	if bs.PortName != "" || bs.Port > 0 {
		rows = append(rows, components.KeyValue{Key: "Port", Value: fmt.Sprintf("%d (%s)", bs.Port, bs.PortName)})
	}
	if bs.TimeoutSec > 0 {
		rows = append(rows, components.KeyValue{Key: "Timeout", Value: fmt.Sprintf("%ds", bs.TimeoutSec)})
	}
	if bs.SessionAffinity != "" {
		rows = append(rows, components.KeyValue{Key: "Session Affinity", Value: bs.SessionAffinity})
	}
	rows = append(rows, components.KeyValue{Key: "CDN Enabled", Value: fmt.Sprintf("%t", bs.EnableCDN)})
	if bs.SecurityPolicy != "" {
		rows = append(rows, components.KeyValue{Key: "Security Policy", Value: bs.SecurityPolicy})
	}
	if bs.CreationTimestamp != "" {
		rows = append(rows, components.KeyValue{Key: "Created", Value: bs.CreationTimestamp})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Backend Service Details",
		Rows:       rows,
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

func (s *Service) renderHealthDetailView() string {
	if s.selectedHealth == nil {
		return "No health check selected"
	}
	hc := s.selectedHealth

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Health Checks",
		hc.Name,
	)

	description := hc.Description
	if description == "" {
		description = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Health Check Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: hc.Name},
			{Key: "Description", Value: description},
			{Key: "Region", Value: hc.Region},
			{Key: "Type", Value: hc.Type},
			{Key: "Port", Value: fmt.Sprintf("%d", hc.Port)},
			{Key: "Check Interval", Value: fmt.Sprintf("%ds", hc.CheckIntervalSec)},
			{Key: "Timeout", Value: fmt.Sprintf("%ds", hc.TimeoutSec)},
			{Key: "Healthy Threshold", Value: fmt.Sprintf("%d", hc.HealthyThreshold)},
			{Key: "Unhealthy Threshold", Value: fmt.Sprintf("%d", hc.UnhealthyThreshold)},
			{Key: "Logging Enabled", Value: fmt.Sprintf("%t", hc.LogEnabled)},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

func (s *Service) renderUrlMapDetailView() string {
	if s.selectedUrlMap == nil {
		return "No URL map selected"
	}
	um := s.selectedUrlMap

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"URL Maps",
		um.Name,
	)

	description := um.Description
	if description == "" {
		description = "-"
	}
	defaultService := um.DefaultService
	if defaultService == "" {
		defaultService = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "URL Map Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: um.Name},
			{Key: "Region", Value: um.Region},
			{Key: "Default Service", Value: defaultService},
			{Key: "Description", Value: description},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

func (s *Service) renderForwardingRuleDetailView() string {
	if s.selectedFwdRule == nil {
		return "No forwarding rule selected"
	}
	fr := s.selectedFwdRule

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Forwarding Rules",
		fr.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Forwarding Rule Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: fr.Name},
			{Key: "Region", Value: fr.Region},
			{Key: "IP Address", Value: fr.IPAddress},
			{Key: "IP Protocol", Value: fr.IPProtocol},
			{Key: "Port Range", Value: fr.PortRange},
			{Key: "Target", Value: fr.Target},
			{Key: "Load Balancing Scheme", Value: fr.LoadBalancingScheme},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

func (s *Service) renderSslCertDetailView() string {
	if s.selectedSslCert == nil {
		return "No SSL certificate selected"
	}
	cert := s.selectedSslCert

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"SSL Certificates",
		cert.Name,
	)

	domains := "-"
	if len(cert.Domains) > 0 {
		domains = strings.Join(cert.Domains, ", ")
	}
	expireTime := cert.ExpireTime
	if expireTime == "" {
		expireTime = "-"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "SSL Certificate Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: cert.Name},
			{Key: "Region", Value: cert.Region},
			{Key: "Type", Value: cert.Type},
			{Key: "Domains", Value: domains},
			{Key: "Expires", Value: expireTime},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

// deleteBackendCmd triggers deletion of the given backend service
func (s *Service) deleteBackendCmd(bs BackendService) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteBackendService(s.projectID, bs.Region, bs.Name); err != nil {
			return actionResultMsg{err: err, resource: "backend service", name: bs.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting backend service %s...", bs.Name), resource: "backend service", name: bs.Name, action: "delete"}
	}
}

// deleteHealthCheckCmd triggers deletion of the given health check
func (s *Service) deleteHealthCheckCmd(hc HealthCheck) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteHealthCheck(s.projectID, hc.Region, hc.Name); err != nil {
			return actionResultMsg{err: err, resource: "health check", name: hc.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting health check %s...", hc.Name), resource: "health check", name: hc.Name, action: "delete"}
	}
}

// deleteUrlMapCmd triggers deletion of the given URL map.
func (s *Service) deleteUrlMapCmd(um UrlMap) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteUrlMap(s.projectID, um.Region, um.Name); err != nil {
			return actionResultMsg{err: err, resource: "URL map", name: um.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting URL map %s...", um.Name), resource: "URL map", name: um.Name, action: "delete"}
	}
}

// deleteForwardingRuleCmd triggers deletion of the given forwarding rule.
func (s *Service) deleteForwardingRuleCmd(fr ForwardingRule) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteForwardingRule(s.projectID, fr.Region, fr.Name); err != nil {
			return actionResultMsg{err: err, resource: "forwarding rule", name: fr.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting forwarding rule %s...", fr.Name), resource: "forwarding rule", name: fr.Name, action: "delete"}
	}
}

// deleteSslCertificateCmd triggers deletion of the given SSL certificate.
func (s *Service) deleteSslCertificateCmd(cert SslCertificate) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteSslCertificate(s.projectID, cert.Region, cert.Name); err != nil {
			return actionResultMsg{err: err, resource: "SSL certificate", name: cert.Name, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting SSL certificate %s...", cert.Name), resource: "SSL certificate", name: cert.Name, action: "delete"}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchBackendServicesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("loadbalancing_backends:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]BackendService); ok {
					return backendServicesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListBackendServices(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return backendServicesMsg(items)
	}
}

func (s *Service) fetchHealthChecksCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("loadbalancing_healthchecks:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]HealthCheck); ok {
					return healthChecksMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListHealthChecks(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return healthChecksMsg(items)
	}
}

func (s *Service) fetchUrlMapsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("loadbalancing_urlmaps:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]UrlMap); ok {
					return urlMapsMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListUrlMaps(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return urlMapsMsg(items)
	}
}

func (s *Service) fetchForwardingRulesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("loadbalancing_fwdrules:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]ForwardingRule); ok {
					return forwardingRulesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListForwardingRules(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return forwardingRulesMsg(items)
	}
}

func (s *Service) fetchSslCertificatesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("loadbalancing_sslcerts:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]SslCertificate); ok {
					return sslCertificatesMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListSslCertificates(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return sslCertificatesMsg(items)
	}
}

func (s *Service) updateBackendTable() {
	rows := make([]table.Row, len(s.backendServices))
	for i, bs := range s.backendServices {
		rows[i] = table.Row{
			bs.Name,
			bs.Region,
			bs.Protocol,
			bs.LoadBalancingScheme,
			bs.HealthCheckName,
			fmt.Sprintf("%d", bs.BackendCount),
		}
	}
	s.backendTable.SetRows(rows)
}

func (s *Service) updateHealthTable() {
	rows := make([]table.Row, len(s.healthChecks))
	for i, hc := range s.healthChecks {
		rows[i] = table.Row{
			hc.Name,
			hc.Region,
			hc.Type,
			fmt.Sprintf("%d", hc.Port),
			fmt.Sprintf("%d", hc.CheckIntervalSec),
			fmt.Sprintf("%d/%d", hc.HealthyThreshold, hc.UnhealthyThreshold),
		}
	}
	s.healthTable.SetRows(rows)
}

func (s *Service) updateUrlMapTable() {
	rows := make([]table.Row, len(s.urlMaps))
	for i, um := range s.urlMaps {
		rows[i] = table.Row{
			um.Name,
			um.Region,
			um.DefaultService,
		}
	}
	s.urlMapTable.SetRows(rows)
}

func (s *Service) updateFwdRuleTable() {
	rows := make([]table.Row, len(s.forwardingRules))
	for i, fr := range s.forwardingRules {
		rows[i] = table.Row{
			fr.Name,
			fr.Region,
			fr.IPAddress,
			fr.IPProtocol,
			fr.PortRange,
			fr.Target,
		}
	}
	s.fwdRuleTable.SetRows(rows)
}

func (s *Service) updateSslCertTable() {
	rows := make([]table.Row, len(s.sslCertificates))
	for i, cert := range s.sslCertificates {
		rows[i] = table.Row{
			cert.Name,
			cert.Region,
			cert.Type,
			strings.Join(cert.Domains, ", "),
		}
	}
	s.sslCertTable.SetRows(rows)
}
