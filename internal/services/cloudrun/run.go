package cloudrun

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

// newServiceUpdateForm builds the FormModel for updating a Cloud Run
// service's container image, seeded with the service's current image. This
// is the only field this Update flow touches -- update-traffic and full
// service replace (env vars, resources, concurrency, ingress, etc.) are
// explicitly out of scope.
func newServiceUpdateForm(svc RunService) components.FormModel {
	return components.NewForm("Update Cloud Run Service: "+svc.Name, []components.FormField{
		{Label: "Container Image", Default: svc.Image, Placeholder: "gcr.io/my-project/my-image:latest", Required: true},
	})
}

// servicesMsg is the message used to pass fetched data
type servicesMsg []RunService

// functionsMsg is the message used to pass fetched functions
type functionsMsg []Function

// errMsg is the standard error message
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g. service creation)
type actionResultMsg struct {
	err error
	msg string
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

	viewState       ViewState
	selectedService *RunService
	selectedFunc    *Function

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// Create State
	createForm components.FormModel

	// Update State
	updateForm components.FormModel

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

	svc := &Service{
		table:     t,
		funcTable: ft,
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
			return "[]:Tabs  r:Refresh  /:Filter  l:Logs  Ent:Detail  n:Create  u:Update  d:Delete"
		}
		return "[]:Tabs  r:Refresh  /:Filter  l:Logs  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		if s.activeTab == TabServices {
			return "Esc/q:Back  u:Update  d:Delete"
		}
		return "Esc/q:Back"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
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
	s.err = nil          // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0) // Reset table position
	s.funcTable.SetCursor(0)
	s.activeTab = TabServices // Default to Services tab
	s.filter.ExitFilterMode()
}

// IsRootView returns true if we are at the top-level list
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// Focus handles input focus (Visual Highlight)
func (s *Service) Focus() {
	s.table.Focus()
	s.funcTable.Focus()
}

// Blur handles loss of input focus (Visual Dimming)
func (s *Service) Blur() {
	s.table.Blur()
	s.funcTable.Blur()
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

	case actionResultMsg:
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

	// 4. Window Resize
	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)
		s.funcTable.HandleWindowSizeDefault(msg)

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
				// Switch Tab (Cycle)
				s.filter.ExitFilterMode()
				if s.activeTab == TabServices {
					s.activeTab = TabFunctions
					s.functionFilterSession.Apply(s.functions)
					return s, tea.Batch(s.fetchFunctionsCmd(true), s.spinner.Start(""))
				} else {
					s.activeTab = TabServices
					s.serviceFilterSession.Apply(s.services)
					return s, nil
				}
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
			}

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedService != nil {
					actionCmd = s.DeleteServiceCmd(*s.selectedService)
				}
				s.viewState = ViewList
				s.selectedService = nil
				s.pendingAction = ""
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
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
				return s, s.UpdateServiceCmd(svc.Name, svc.Region, vals["Container Image"])
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

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	// Default: List View
	return s.renderWithTabs()
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

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Service Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: svc.Name},
			{Key: "Region", Value: svc.Region},
			{Key: "Status", Value: components.RenderStatus(string(svc.Status))},
			{Key: "URL", Value: svc.URL},
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

// renderConfirmation renders the delete confirmation dialog for services.
func (s *Service) renderConfirmation() string {
	if s.selectedService == nil {
		return "Error: No service selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedService.Name, "service")
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
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateService(s.projectID, region, name, image, port); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating service %s...", name)}
	}
}

// UpdateServiceCmd fires the UpdateServiceImage API call for the given
// service, updating only its container image.
func (s *Service) UpdateServiceCmd(name, region, image string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateServiceImage(s.projectID, region, name, image); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating service %s...", name)}
	}
}

// DeleteServiceCmd triggers deletion of the given Cloud Run service
func (s *Service) DeleteServiceCmd(svc RunService) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteService(s.projectID, svc.Region, svc.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting service %s...", svc.Name)}
	}
}

func (s *Service) fetchDataCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "cloudrun_services"

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

func (s *Service) fetchFunctionsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "cloudrun_functions"
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
