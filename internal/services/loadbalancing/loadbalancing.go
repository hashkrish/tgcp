// Package loadbalancing (this file) implements the Service UI for Cloud
// Load Balancing.
//
// Scope: Load Balancing spans many resource types in the real product
// (backend services, backend buckets, URL maps, forwarding rules, target
// proxies, SSL certificates, ...). Per TODO.md's guidance to find "the
// minimum useful slice first" rather than trying to cover everything
// `gcloud compute` exposes, this MVP covers exactly two tabs:
//
//   - Backend Services: the resource that actually groups backends behind
//     a health check and is common to every LB type (external/internal,
//     HTTP(S)/TCP/UDP). Understanding backend health/config is usually the
//     first thing you reach for when debugging a load balancer.
//   - Health Checks: directly referenced by backend services, and useful
//     to browse independently (a health check can be shared by multiple
//     backend services).
//
// URL maps, forwarding rules, and SSL certificates are deliberately left
// out of this pass — they're more about routing/exposure than backend
// health, and each would need its own tab plus cross-referencing logic to
// be useful, which is a larger design surface than this MVP scope.
package loadbalancing

import (
	"context"
	"fmt"
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
)

type Tab int

const (
	TabBackendServices Tab = iota
	TabHealthChecks
)

type backendServicesMsg []BackendService
type healthChecksMsg []HealthCheck
type errMsg error

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	// Tables
	backendTable *components.StandardTable
	healthTable  *components.StandardTable

	activeTab Tab

	// State
	backendServices []BackendService
	healthChecks    []HealthCheck
	spinner         components.SpinnerModel
	err             error

	viewState       ViewState
	selectedBackend *BackendService
	selectedHealth  *HealthCheck

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

	return &Service{
		backendTable: bTable,
		healthTable:  hTable,
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
		return "[]:Tabs  r:Refresh  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back"
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

func (s *Service) Refresh() tea.Cmd {
	var fetchCmd tea.Cmd
	if s.activeTab == TabBackendServices {
		fetchCmd = s.fetchBackendServicesCmd(true)
	} else {
		fetchCmd = s.fetchHealthChecksCmd(true)
	}
	return tea.Batch(s.spinner.Start(""), fetchCmd)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedBackend = nil
	s.selectedHealth = nil
	s.err = nil
	s.backendTable.SetCursor(0)
	s.healthTable.SetCursor(0)
	s.activeTab = TabBackendServices
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.backendTable.Focus()
	s.healthTable.Focus()
}

func (s *Service) Blur() {
	s.backendTable.Blur()
	s.healthTable.Blur()
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
		var batch []tea.Cmd
		if s.activeTab == TabBackendServices {
			batch = append(batch, s.fetchBackendServicesCmd(false))
		} else {
			batch = append(batch, s.fetchHealthChecksCmd(false))
		}
		batch = append(batch, s.tick())
		return s, tea.Batch(batch...)

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

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case tea.WindowSizeMsg:
		s.backendTable.HandleWindowSizeDefault(msg)
		s.healthTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			if s.activeTab == TabBackendServices {
				updatedTable, cmd = s.backendTable.Update(msg)
				s.backendTable = updatedTable
			} else {
				updatedTable, cmd = s.healthTable.Update(msg)
				s.healthTable = updatedTable
			}
			return s, cmd
		}

	case tea.KeyMsg:
		switch s.viewState {
		case ViewList:
			switch msg.String() {
			case "[", "]":
				if s.activeTab == TabBackendServices {
					s.activeTab = TabHealthChecks
					if s.healthChecks == nil {
						return s, tea.Batch(s.fetchHealthChecksCmd(false), s.spinner.Start(""))
					}
				} else {
					s.activeTab = TabBackendServices
					if s.backendServices == nil {
						return s, tea.Batch(s.fetchBackendServicesCmd(false), s.spinner.Start(""))
					}
				}
				return s, nil
			case "r":
				return s, s.Refresh()
			case "enter":
				if s.activeTab == TabBackendServices {
					if idx := s.backendTable.Cursor(); idx >= 0 && idx < len(s.backendServices) {
						s.selectedBackend = &s.backendServices[idx]
						s.viewState = ViewDetail
					}
				} else {
					if idx := s.healthTable.Cursor(); idx >= 0 && idx < len(s.healthChecks) {
						s.selectedHealth = &s.healthChecks[idx]
						s.viewState = ViewDetail
					}
				}
			}
			var updatedTable *components.StandardTable
			if s.activeTab == TabBackendServices {
				updatedTable, cmd = s.backendTable.Update(msg)
				s.backendTable = updatedTable
			} else {
				updatedTable, cmd = s.healthTable.Update(msg)
				s.healthTable = updatedTable
			}
			return s, cmd
		case ViewDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewList
				s.selectedBackend = nil
				s.selectedHealth = nil
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

	return s.renderWithTabs()
}

func (s *Service) renderWithTabs() string {
	var tabs string
	var tableView string
	listLabel := "Backend Services"

	if s.activeTab == TabBackendServices {
		tabs = lipgloss.JoinHorizontal(lipgloss.Top,
			styles.ActiveTabStyle.Render(" Backend Services "),
			styles.InactiveTabStyle.Render(" Health Checks "),
		)
		if len(s.backendServices) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.backendTable.View()
		}
	} else {
		listLabel = "Health Checks"
		tabs = lipgloss.JoinHorizontal(lipgloss.Top,
			styles.InactiveTabStyle.Render(" Backend Services "),
			styles.ActiveTabStyle.Render(" Health Checks "),
		)
		if len(s.healthChecks) == 0 {
			tableView = components.EmptyState("default")
		} else {
			tableView = s.healthTable.View()
		}
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		listLabel,
	)

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, tabs, tableView)
}

func (s *Service) renderDetailView() string {
	if s.activeTab == TabHealthChecks {
		return s.renderHealthDetailView()
	}
	return s.renderBackendDetailView()
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

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Backend Service Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: bs.Name},
			{Key: "Region", Value: bs.Region},
			{Key: "Protocol", Value: bs.Protocol},
			{Key: "Load Balancing Scheme", Value: bs.LoadBalancingScheme},
			{Key: "Health Check", Value: healthCheck},
			{Key: "Backend Count", Value: fmt.Sprintf("%d", bs.BackendCount)},
		},
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

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Health Check Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: hc.Name},
			{Key: "Region", Value: hc.Region},
			{Key: "Type", Value: hc.Type},
			{Key: "Port", Value: fmt.Sprintf("%d", hc.Port)},
			{Key: "Check Interval", Value: fmt.Sprintf("%ds", hc.CheckIntervalSec)},
			{Key: "Timeout", Value: fmt.Sprintf("%ds", hc.TimeoutSec)},
			{Key: "Healthy Threshold", Value: fmt.Sprintf("%d", hc.HealthyThreshold)},
			{Key: "Unhealthy Threshold", Value: fmt.Sprintf("%d", hc.UnhealthyThreshold)},
		},
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
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
