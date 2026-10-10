package apis

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

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewEnableForm
	ViewConfirmation
)

// tab selects which list is shown: the project's enabled APIs, or the
// curated set of common Google Cloud APIs (see commonAPIs) in either state.
type tab int

const (
	tabEnabled tab = iota
	tabCommon
)

type apisMsg struct {
	tab   tab
	items []API
}
type errMsg error

// actionResultMsg carries the result of an enable/disable action.
type actionResultMsg struct {
	name     string
	newState string // state the API is in after a successful action
	err      error
	msg      string
}

// enableFormField is the label of the single field in the enable-by-name form.
const enableFormField = "Service name"

func newEnableForm() components.FormModel {
	return components.NewForm("Enable API", []components.FormField{
		{Label: enableFormField, Placeholder: "compute.googleapis.com", Required: true},
	})
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[API]

	activeTab tab
	enabled   []API
	common    []API
	loaded    map[tab]bool // whether each tab's list has been fetched

	spinner components.SpinnerModel
	err     error

	viewState ViewState
	selected  *API

	enableForm components.FormModel

	// Confirmation State
	pendingAction string // "enable", "disable", "force-disable"
	pendingName   string
	actionSource  ViewState

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Title", Width: 40},
		{Title: "Service Name", Width: 40},
		{Title: "State", Width: 12},
	}

	svc := &Service{
		table:     components.NewStandardTable(columns),
		filter:    components.NewFilterWithPlaceholder("Filter APIs..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		loaded:    map[tab]bool{},
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, getFilteredAPIs, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "APIs & Services"
}

func (s *Service) ShortName() string {
	return "apis"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewList:
		return "r:Refresh  /:Filter  Tab:Enabled/Common  e:Enable  x:Disable  n:Enable by Name  Ent:Detail"
	case ViewDetail:
		return "Esc/q:Back  e:Enable  x:Disable  X:Disable + Dependents"
	case ViewEnableForm:
		return "Enter/Ctrl+S Submit  Esc Cancel"
	case ViewConfirmation:
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

func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	s.activeTab = tabEnabled
	s.enabled = nil
	s.common = nil
	s.loaded = map[tab]bool{}
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
		s.fetchCmd(s.activeTab, true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selected = nil
	s.err = nil
	s.pendingAction = ""
	s.pendingName = ""
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

// NextTab/PrevTab implement services.TabCycler. With only two tabs both
// directions simply toggle.
func (s *Service) NextTab() (tea.Cmd, bool) {
	if !s.IsRootView() || s.filter.IsActive() {
		return nil, false
	}
	return s.switchTab(), true
}

func (s *Service) PrevTab() (tea.Cmd, bool) {
	return s.NextTab()
}

func (s *Service) switchTab() tea.Cmd {
	if s.activeTab == tabEnabled {
		s.activeTab = tabCommon
	} else {
		s.activeTab = tabEnabled
	}
	s.filter.ExitFilterMode()
	s.table.SetCursor(0)
	s.filterSession.Apply(s.currentItems())
	if !s.loaded[s.activeTab] {
		return tea.Batch(s.spinner.Start(""), s.fetchCmd(s.activeTab, false))
	}
	return nil
}

func (s *Service) currentItems() []API {
	if s.activeTab == tabCommon {
		return s.common
	}
	return s.enabled
}

// cursorAPI returns the API under the table cursor, honouring the filter.
func (s *Service) cursorAPI() *API {
	items := getFilteredAPIs(s.currentItems(), s.filter.Value())
	if idx := s.table.Cursor(); idx >= 0 && idx < len(items) {
		a := items[idx]
		return &a
	}
	return nil
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
		return s, tea.Batch(s.fetchCmd(s.activeTab, false), s.tick())

	case apisMsg:
		s.spinner.Stop()
		if msg.tab == tabCommon {
			s.common = msg.items
		} else {
			s.enabled = msg.items
		}
		s.loaded[msg.tab] = true
		if msg.tab == s.activeTab {
			s.filterSession.Apply(s.currentItems())
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		if s.selected != nil && s.selected.Name == msg.name {
			s.selected.State = msg.newState
		}
		// Both lists are now stale: drop their cache entries and make the
		// other tab refetch the next time it's shown.
		if s.cache != nil {
			s.cache.Delete(cacheKey(s.projectID, tabEnabled))
			s.cache.Delete(cacheKey(s.projectID, tabCommon))
		}
		s.loaded = map[tab]bool{}
		return s, tea.Batch(
			func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
			s.fetchCmd(s.activeTab, true),
		)

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
		return s.handleKey(msg)
	}

	return s, nil
}

func (s *Service) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch s.viewState {
	case ViewEnableForm:
		result, formCmd := s.enableForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewList
			return s, nil
		}
		if result.Submitted {
			s.askConfirm("enable", s.enableForm.Value(enableFormField), ViewList)
			return s, nil
		}
		return s, formCmd

	case ViewList:
		result := s.filterSession.HandleKey(msg)
		if result.Handled {
			if result.Cmd != nil {
				return s, result.Cmd
			}
			if !result.ShouldContinue {
				return s, nil
			}
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "[", "]":
			return s, s.switchTab()
		case "n":
			s.enableForm = newEnableForm()
			s.viewState = ViewEnableForm
			return s, nil
		case "enter":
			if a := s.cursorAPI(); a != nil {
				s.selected = a
				s.viewState = ViewDetail
			}
			return s, nil
		case "e":
			if a := s.cursorAPI(); a != nil && a.State != "ENABLED" {
				s.askConfirm("enable", a.Name, ViewList)
			}
			return s, nil
		case "x":
			if a := s.cursorAPI(); a != nil && a.State == "ENABLED" {
				s.askConfirm("disable", a.Name, ViewList)
			}
			return s, nil
		}
		var cmd tea.Cmd
		s.table, cmd = s.table.Update(msg)
		return s, cmd

	case ViewDetail:
		if s.selected == nil {
			s.viewState = ViewList
			return s, nil
		}
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewList
			s.selected = nil
		case "e":
			if s.selected.State != "ENABLED" {
				s.askConfirm("enable", s.selected.Name, ViewDetail)
			}
		case "x":
			if s.selected.State == "ENABLED" {
				s.askConfirm("disable", s.selected.Name, ViewDetail)
			}
		case "X":
			if s.selected.State == "ENABLED" {
				s.askConfirm("force-disable", s.selected.Name, ViewDetail)
			}
		}
		return s, nil

	case ViewConfirmation:
		switch msg.String() {
		case "y", "enter":
			action, name := s.pendingAction, s.pendingName
			s.viewState = s.actionSource
			s.pendingAction, s.pendingName = "", ""
			return s, tea.Batch(s.spinner.Start(spinnerText(action, name)), s.actionCmd(action, name))
		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction, s.pendingName = "", ""
		}
		return s, nil
	}

	return s, nil
}

func (s *Service) askConfirm(action, name string, source ViewState) {
	s.pendingAction = action
	s.pendingName = name
	s.actionSource = source
	s.viewState = ViewConfirmation
}

func spinnerText(action, name string) string {
	if action == "enable" {
		return fmt.Sprintf("Enabling %s...", name)
	}
	return fmt.Sprintf("Disabling %s...", name)
}

// -----------------------------------------------------------------------------
// Actions
// -----------------------------------------------------------------------------

func (s *Service) actionCmd(action, name string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "api", Name: name, Action: action,
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			switch action {
			case "enable":
				return s.client.EnableAPI(s.projectID, name)
			case "disable":
				return s.client.DisableAPI(s.projectID, name, false)
			case "force-disable":
				return s.client.DisableAPI(s.projectID, name, true)
			}
			return fmt.Errorf("unknown action: %s", action)
		})
		if err != nil {
			return actionResultMsg{name: name, err: fmt.Errorf("failed to %s %s: %w", action, name, err)}
		}
		if action == "enable" {
			return actionResultMsg{name: name, newState: "ENABLED", msg: fmt.Sprintf("Enabled %s", name)}
		}
		return actionResultMsg{name: name, newState: "DISABLED", msg: fmt.Sprintf("Disabled %s", name)}
	}
}

// -----------------------------------------------------------------------------
// Data
// -----------------------------------------------------------------------------

func cacheKey(projectID string, t tab) string {
	if t == tabCommon {
		return fmt.Sprintf("apis_common:%s", projectID)
	}
	return fmt.Sprintf("apis_enabled:%s", projectID)
}

func (s *Service) fetchCmd(t tab, force bool) tea.Cmd {
	projectID := s.projectID
	return func() tea.Msg {
		key := cacheKey(projectID, t)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]API); ok {
					return apisMsg{tab: t, items: items}
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		var items []API
		var err error
		if t == tabCommon {
			items, err = s.client.ListCommonAPIs(projectID)
		} else {
			items, err = s.client.ListEnabledAPIs(projectID)
		}
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return apisMsg{tab: t, items: items}
	}
}

func (s *Service) updateTable(items []API) {
	rows := make([]table.Row, len(items))
	for i, a := range items {
		rows[i] = table.Row{
			displayTitle(a),
			a.Name,
			stateText(a.State),
		}
	}
	s.table.SetRows(rows)
}

// stateText renders an API's state as a plain icon+text string for the
// StandardTable. components.RenderStatus is a padded, background-colored
// badge whose ANSI escapes bubbles/table's fixed-width column truncation
// doesn't account for (the cell comes out blank), so table cells stay plain.
func stateText(state string) string {
	icon := components.IconUnknown
	switch components.CategorizeStatus(state) {
	case components.StatusRunning:
		icon = components.IconRunning
	case components.StatusStopped:
		icon = components.IconStopped
	case components.StatusPending:
		icon = components.IconPending
	}
	return icon + " " + strings.ToUpper(strings.TrimSpace(state))
}

func getFilteredAPIs(items []API, query string) []API {
	if query == "" {
		return items
	}
	return components.FilterSlice(items, query, func(a API, q string) bool {
		return components.ContainsMatch(a.Title, a.Name, a.State)(q)
	})
}
