package net

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 5 * time.Minute

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

// newFirewallUpdateForm builds the FormModel for updating a firewall
// rule's priority, seeded with its current value. Action, ports,
// source/target ranges, and expand-ip-range are out of scope.
func newFirewallUpdateForm(fw Firewall) components.FormModel {
	return components.NewForm("Update Firewall Rule: "+fw.Name, []components.FormField{
		{Label: "Priority", Default: fmt.Sprintf("%d", fw.Priority), Required: true, Validate: func(v string) string {
			if _, err := strconv.ParseInt(v, 10, 64); err != nil {
				return "must be an integer"
			}
			return ""
		}},
	})
}

type Tab int

const (
	TabSubnets Tab = iota
	TabFirewalls
)

type networksMsg []Network
type subnetsMsg []Subnet
type firewallsMsg []Firewall
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g.
// firewall rule creation).
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

	// Tables
	networksTable  *components.StandardTable
	subnetsTable   *components.StandardTable
	firewallsTable *components.StandardTable

	// UI
	activeTab Tab

	// State
	networks  []Network
	subnets   []Subnet
	firewalls []Firewall
	spinner   components.SpinnerModel
	err       error

	viewState        ViewState
	selectedNetwork  *Network
	selectedFirewall *Firewall

	createForm     components.FormModel
	createReturnTo ViewState

	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Networks Table
	nCols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Mode", Width: 10},
		{Title: "IPv4 Range", Width: 20},
		{Title: "Gateway", Width: 15},
	}
	nTable := components.NewStandardTable(nCols)

	// Subnets Table
	sCols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Region", Width: 15},
		{Title: "Range", Width: 15},
		{Title: "Gateway", Width: 15},
	}
	sTable := components.NewStandardTable(sCols)

	// Firewalls Table
	fCols := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Type", Width: 8},
		{Title: "Action", Width: 6},
		{Title: "Priority", Width: 8},
		{Title: "Source", Width: 20},
		{Title: "Target", Width: 20},
	}
	fTable := components.NewStandardTable(fCols)

	return &Service{
		networksTable:  nTable,
		subnetsTable:   sTable,
		firewallsTable: fTable,
		spinner:        components.NewSpinner(),
		viewState:      ViewList,
		activeTab:      TabSubnets,
		cache:          cache,
	}
}

func (s *Service) Name() string      { return "Networking" }
func (s *Service) ShortName() string { return "net" }

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "Ent:Detail  r:Refresh"
	}
	if s.viewState == ViewDetail {
		if s.activeTab == TabFirewalls {
			return "[]:Switch Tab  Esc/q:Back  n:New Firewall Rule  u:Update  d:Delete"
		}
		return "[]:Switch Tab  Esc/q:Back  n:New Firewall Rule"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
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

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (s *Service) Refresh() tea.Cmd {
	var fetchCmd tea.Cmd
	switch s.viewState {
	case ViewList:
		fetchCmd = s.fetchNetworksCmd()
	case ViewDetail:
		fetchCmd = tea.Batch(s.fetchSubnetsCmd(), s.fetchFirewallsCmd())
	}
	if fetchCmd == nil {
		return nil
	}
	return tea.Batch(
		s.spinner.Start(""),
		fetchCmd,
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedNetwork = nil
	s.activeTab = TabSubnets
	s.err = nil
	s.networksTable.SetCursor(0)
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

func (s *Service) Focus() {
	s.networksTable.Focus()
	s.subnetsTable.Focus()
	s.firewallsTable.Focus()
}

func (s *Service) Blur() {
	s.networksTable.Blur()
	s.subnetsTable.Blur()
	s.firewallsTable.Blur()
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
		if s.viewState == ViewList {
			return s, tea.Batch(s.fetchNetworksCmd(), s.Init())
		}
		return s, tea.Batch(s.fetchSubnetsCmd(), s.fetchFirewallsCmd(), s.Init())

	case networksMsg:
		s.spinner.Stop()
		s.networks = msg
		s.updateNetworksTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case subnetsMsg:
		s.spinner.Stop()
		s.subnets = msg
		s.updateSubnetsTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case firewallsMsg:
		s.spinner.Stop()
		s.firewalls = msg
		s.updateFirewallsTable()
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
			s.selectedFirewall = nil
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
		s.networksTable.HandleWindowSizeDefault(msg)
		// Tab headers take extra space for detail view tables
		s.subnetsTable.HandleWindowSize(msg, 9)
		s.firewallsTable.HandleWindowSize(msg, 9)

	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.networksTable.Update(msg)
			s.networksTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.createReturnTo
				return s, nil
			}
			if result.Submitted {
				return s, s.createFirewallCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedFirewall != nil {
				return s, s.updateFirewallCmd(*s.selectedFirewall)
			}
			return s, formCmd
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		}

		switch s.viewState {
		case ViewList:
			if msg.String() == "enter" {
				if s.networksTable.Cursor() >= 0 && s.networksTable.Cursor() < len(s.networks) {
					s.selectedNetwork = &s.networks[s.networksTable.Cursor()]
					s.viewState = ViewDetail
					s.activeTab = TabSubnets // Default to subnets
					return s, tea.Batch(s.fetchSubnetsCmd(), s.fetchFirewallsCmd(), s.spinner.Start(""))
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.networksTable.Update(msg)
			s.networksTable = updatedTable
			return s, cmd

		case ViewDetail:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewList
				s.selectedNetwork = nil
				return s, nil
			case "n": // New firewall rule
				network := "default"
				if s.selectedNetwork != nil {
					network = s.selectedNetwork.Name
				}
				s.createForm = components.NewForm("New Firewall Rule", []components.FormField{
					{Label: "Name", Required: true},
					{Label: "Network", Default: network, Required: true},
					{Label: "Direction", Default: "INGRESS", Required: true},
					{Label: "Action", Default: "ALLOW", Required: true},
					{Label: "Protocol", Default: "tcp", Required: true},
					{Label: "Ports", Default: "80,443"},
					{Label: "Source Ranges", Default: "0.0.0.0/0"},
				})
				s.createReturnTo = ViewDetail
				s.viewState = ViewCreate
				return s, nil
			case "[", "]", "tab": // Allow tab-like switching
				if s.activeTab == TabSubnets {
					s.activeTab = TabFirewalls
				} else {
					s.activeTab = TabSubnets
				}
				return s, nil
			case "u": // Update firewall rule priority (Firewalls tab only)
				if s.activeTab == TabFirewalls {
					if idx := s.firewallsTable.Cursor(); idx >= 0 && idx < len(s.firewalls) {
						s.selectedFirewall = &s.firewalls[idx]
						s.updateForm = newFirewallUpdateForm(*s.selectedFirewall)
						s.viewState = ViewUpdate
						return s, nil
					}
				}
			case "d": // Delete firewall rule (Firewalls tab only, Confirm)
				if s.activeTab == TabFirewalls {
					if idx := s.firewallsTable.Cursor(); idx >= 0 && idx < len(s.firewalls) {
						s.selectedFirewall = &s.firewalls[idx]
						s.pendingAction = "delete"
						s.actionSource = ViewDetail
						s.viewState = ViewConfirmation
						return s, nil
					}
				}
			}

			var updatedTable *components.StandardTable
			if s.activeTab == TabSubnets {
				updatedTable, cmd = s.subnetsTable.Update(msg)
				s.subnetsTable = updatedTable
			} else {
				updatedTable, cmd = s.firewallsTable.Update(msg)
				s.firewallsTable = updatedTable
			}
			return s, cmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedFirewall != nil {
					actionCmd = s.deleteFirewallCmd(*s.selectedFirewall)
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
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Networks")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewList:
		breadcrumb := components.Breadcrumb(
			fmt.Sprintf("Project %s", s.projectID),
			s.Name(),
			"Networks",
		)
		if len(s.networks) == 0 {
			return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, components.EmptyState("networks"))
		}
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, s.networksTable.View())
	case ViewDetail:
		return s.renderDetailView()
	case ViewCreate:
		return s.createForm.View()
	case ViewUpdate:
		return s.updateForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	}
	return ""
}

// renderConfirmation renders the firewall-rule-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedFirewall == nil {
		return "Error: No firewall rule selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedFirewall.Name, "firewall rule")
}

// createFirewallCmd fires the CreateFirewallRule API call using the current
// createForm values.
func (s *Service) createFirewallCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := FirewallCreateOpts{
		Name:         v["Name"],
		Network:      v["Network"],
		Direction:    v["Direction"],
		Action:       v["Action"],
		Protocol:     v["Protocol"],
		Ports:        v["Ports"],
		SourceRanges: v["Source Ranges"],
	}
	returnTo := s.createReturnTo
	s.viewState = returnTo
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateFirewallRule(s.projectID, opts); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating firewall rule %s...", opts.Name)}
	}
}

// updateFirewallCmd fires the UpdateFirewallPriority API call using the
// current update-form value.
func (s *Service) updateFirewallCmd(fw Firewall) tea.Cmd {
	priority := fw.Priority
	if v := s.updateForm.Value("Priority"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			priority = n
		}
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateFirewallPriority(s.projectID, fw.Name, priority); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating firewall rule %s...", fw.Name)}
	}
}

func (s *Service) renderDetailView() string {
	header := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Networks",
		s.selectedNetwork.Name,
	)

	// Tabs
	var subStyle, fwStyle lipgloss.Style
	if s.activeTab == TabSubnets {
		subStyle = styles.ActiveTabStyle
		fwStyle = styles.InactiveTabStyle
	} else {
		subStyle = styles.InactiveTabStyle
		fwStyle = styles.ActiveTabStyle
	}

	// Tab labels need explicit leading/trailing spaces now that
	// ActiveTabStyle/InactiveTabStyle are plain text with no padding --
	// without them adjacent labels render with zero separation between
	// them (e.g. "SubnetsFirewall Rules").
	tabs := lipgloss.JoinHorizontal(lipgloss.Top,
		subStyle.Render(" Subnets "),
		fwStyle.Render(" Firewall Rules "),
	)

	var content string
	if s.activeTab == TabSubnets {
		if len(s.subnets) == 0 {
			content = components.EmptyState("subnets")
		} else {
			content = s.subnetsTable.View()
		}
	} else {
		if len(s.firewalls) == 0 {
			content = components.EmptyState("firewalls")
		} else {
			content = s.firewallsTable.View()
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, tabs, content)
}

// deleteFirewallCmd triggers deletion of the given firewall rule
func (s *Service) deleteFirewallCmd(fw Firewall) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteFirewallRule(s.projectID, fw.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting firewall rule %s...", fw.Name)}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchNetworksCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		nets, err := s.client.ListNetworks(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		return networksMsg(nets)
	}
}

func (s *Service) fetchSubnetsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.selectedNetwork == nil {
			return errMsg(fmt.Errorf("no network"))
		}
		subs, err := s.client.ListSubnets(s.projectID, s.selectedNetwork.SelfLink)
		if err != nil {
			return errMsg(err)
		}
		return subnetsMsg(subs)
	}
}

func (s *Service) fetchFirewallsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.selectedNetwork == nil {
			return errMsg(fmt.Errorf("no network"))
		}
		fws, err := s.client.ListFirewalls(s.projectID, s.selectedNetwork.SelfLink)
		if err != nil {
			return errMsg(err)
		}
		return firewallsMsg(fws)
	}
}

func (s *Service) updateNetworksTable() {
	rows := make([]table.Row, len(s.networks))
	for i, n := range s.networks {
		mode := n.Mode
		if mode == "AUTO" {
			mode = "AUTO"
		}
		rows[i] = table.Row{n.Name, mode, n.IPv4Range, n.GatewayIPv4}
	}
	s.networksTable.SetRows(rows)
}

func (s *Service) updateSubnetsTable() {
	rows := make([]table.Row, len(s.subnets))
	for i, sub := range s.subnets {
		rows[i] = table.Row{sub.Name, sub.Region, sub.IPCidrRange, sub.Gateway}
	}
	s.subnetsTable.SetRows(rows)
}

func (s *Service) updateFirewallsTable() {
	rows := make([]table.Row, len(s.firewalls))
	for i, f := range s.firewalls {
		prio := fmt.Sprintf("%d", f.Priority)
		rows[i] = table.Row{f.Name, f.Direction, f.Action, prio, f.Source, f.Target}
	}
	s.firewallsTable.SetRows(rows)
}
