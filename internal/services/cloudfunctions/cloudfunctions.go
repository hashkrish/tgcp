package cloudfunctions

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

// TODO(create): Cloud Functions deploy is intentionally NOT implemented in
// this package's Create pass. Unlike gce/disks/gke/cloudsql/cloudrun, a real
// `functions deploy` (v2 Functions.Create / Patch) requires a source code
// bundle — a GCS object (zip) or a repo/git reference — that the build step
// actually compiles and runs; there is no "empty" or placeholder source that
// produces a working function. Wiring a Create form here without a real
// source upload flow would submit a request that is guaranteed to fail the
// build, i.e. a broken API call, which this pass explicitly avoids. Full
// source upload (zip + GCS staging bucket resolution) is out of scope for
// this minimal Create pass; revisit as a follow-up once that upload flow
// exists.

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCall
	ViewConfirmation
	ViewIAM
	ViewIAMForm
)

type functionsMsg []Function
type errMsg error
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetFunctionIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Function]

	functions []Function
	spinner   components.SpinnerModel
	err       error

	viewState    ViewState
	selectedFunc *Function

	// Call State (Lifecycle) — a single JSON data field, matching
	// `gcloud functions call --data`. Only meaningful for Gen1 functions;
	// see CallFunction's doc comment for why Gen2 is rejected.
	callForm components.FormModel
	callData string

	// Confirmation State
	pendingAction string    // "delete", "call", "grant"
	actionSource  ViewState // Where to return after confirmation

	// IAM State: current bindings for the selected function, and the
	// add-binding form. pendingIAMRole/pendingIAMMember are captured at
	// form-submit time and consumed by the "grant" confirmation.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Region", Width: 15},
		{Title: "Trigger", Width: 14},
		{Title: "Runtime", Width: 14},
		{Title: "Gen", Width: 6},
		{Title: "State", Width: 12},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter functions..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredFunctions, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud Functions"
}

func (s *Service) ShortName() string {
	return "functions"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  d:Delete"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  c:Call  i:IAM  d:Delete"
	}
	if s.viewState == ViewCall || s.viewState == ViewIAMForm {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewIAM {
		return "Esc/q:Back  a:Grant Role"
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

func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchFunctionsCmd(false),
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
		s.fetchFunctionsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedFunc = nil
	s.err = nil
	s.pendingAction = ""
	s.iamBindings = nil
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
		return s, tea.Batch(s.fetchFunctionsCmd(false), s.tick())

	case functionsMsg:
		s.spinner.Stop()
		s.functions = msg
		s.filterSession.Apply(s.functions)
		if s.selectedFunc != nil {
			for i := range s.functions {
				if s.functions[i].FullName == s.selectedFunc.FullName {
					s.selectedFunc = &s.functions[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

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
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		if s.viewState == ViewIAM && s.selectedFunc != nil {
			// A grant just landed -- re-fetch bindings instead of the
			// service-list refresh below, which wouldn't reflect it.
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.fetchIAMCmd(*s.selectedFunc),
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
		if s.viewState == ViewCall {
			result, formCmd := s.callForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedFunc != nil {
				s.callData = s.callForm.Value("Data (JSON)")
				s.pendingAction = "call"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
				return s, nil
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
			case "enter":
				funcs := s.getFilteredFunctions(s.functions, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(funcs) {
					s.selectedFunc = &funcs[idx]
					s.viewState = ViewDetail
				}
			case "d": // Delete (Confirm)
				funcs := s.getFilteredFunctions(s.functions, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(funcs) {
					s.selectedFunc = &funcs[idx]
					s.pendingAction = "delete"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
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
				s.selectedFunc = nil
				return s, nil
			case "c": // Call (open data-payload form)
				if s.selectedFunc != nil {
					s.callForm = components.NewForm("Call Function: "+s.selectedFunc.Name, []components.FormField{
						{Label: "Data (JSON)", Default: "{}"},
					})
					s.viewState = ViewCall
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedFunc != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // IAM
				if s.selectedFunc != nil {
					return s, tea.Batch(s.fetchIAMCmd(*s.selectedFunc), s.spinner.Start(""))
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAM {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "a":
				if s.selectedFunc != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedFunc.Name)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.selectedFunc != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "call" && s.selectedFunc != nil {
					actionCmd = s.CallFunctionCmd(*s.selectedFunc, s.callData)
					s.viewState = ViewDetail
					s.pendingAction = ""
					return s, actionCmd
				}
				if s.pendingAction == "grant" && s.selectedFunc != nil {
					actionCmd = s.addIAMBindingCmd(*s.selectedFunc, s.pendingIAMRole, s.pendingIAMMember)
					s.viewState = ViewIAM
					s.pendingAction = ""
					return s, actionCmd
				}
				if s.pendingAction == "delete" && s.selectedFunc != nil {
					actionCmd = s.DeleteFunctionCmd(*s.selectedFunc)
				}
				s.viewState = ViewList
				s.selectedFunc = nil
				s.pendingAction = ""
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
		return components.RenderError(s.err, s.Name(), "Functions")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewCall {
		return s.callForm.View()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}

	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}

	return s.renderListView()
}

// renderIAMView renders the current IAM policy bindings for the selected
// function, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedFunc == nil {
		return "Error: No function selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Functions",
		s.selectedFunc.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedFunc.Name, rows)
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Functions",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.functions) == 0 {
		content.WriteString(components.EmptyState("default"))
		return content.String()
	}
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailView() string {
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

	triggerDetail := f.TriggerDetail
	if triggerDetail == "" {
		triggerDetail = "-"
	}
	url := f.URL
	if url == "" {
		url = "-"
	}
	source := f.SourceLocation
	if source == "" {
		source = "-"
	}
	entryPoint := f.EntryPoint
	if entryPoint == "" {
		entryPoint = "-"
	}
	cpu := f.CPU
	if cpu == "" {
		cpu = "-"
	}
	memory := f.Memory
	if memory == "" {
		memory = "-"
	}

	rows := []components.KeyValue{
		{Key: "Name", Value: f.Name},
		{Key: "Region", Value: f.Region},
		{Key: "Generation", Value: f.Environment},
		{Key: "State", Value: f.State},
		{Key: "Runtime", Value: f.Runtime},
		{Key: "Entry Point", Value: entryPoint},
		{Key: "Trigger Type", Value: f.TriggerType},
		{Key: "Trigger Detail", Value: triggerDetail},
		{Key: "Memory", Value: memory},
		{Key: "CPU", Value: cpu},
		{Key: "Env Vars Set", Value: fmt.Sprintf("%d", f.EnvVarCount)},
		{Key: "Source", Value: source},
		{Key: "URL", Value: url},
	}
	if !f.UpdateTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Last Updated", Value: f.UpdateTime.Local().Format("2006-01-02 15:04:05 MST")})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Function Details",
		Rows:       rows,
		FooterHint: "Press 'q' or 'esc' to return",
	})

	return fmt.Sprintf("%s\n\n%s", title, card)
}

// renderConfirmation renders the delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedFunc == nil {
		return "Error: No function selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedFunc.Name, "function")
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// DeleteFunctionCmd triggers deletion of the given Cloud Function
func (s *Service) DeleteFunctionCmd(fn Function) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteFunction(fn.FullName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting function %s...", fn.Name)}
	}
}

// CallFunctionCmd triggers a synchronous invocation of the given function
// with the given JSON data payload. Only Gen1 functions are actually
// callable here — see Client.CallFunction's doc comment.
func (s *Service) CallFunctionCmd(fn Function, data string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		result, err := s.client.CallFunction(fn, data)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Called %s: %s", fn.Name, result)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a function.
func (s *Service) fetchIAMCmd(fn Function) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetFunctionIAMPolicy(fn.FullName)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given function.
func (s *Service) addIAMBindingCmd(fn Function, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddFunctionIAMBinding(fn.FullName, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on function %s", role, member, fn.Name)}
	}
}

func (s *Service) fetchFunctionsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("cloudfunctions:%s", s.projectID)

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

func (s *Service) updateTable(items []Function) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		gen := "Gen1"
		if item.Environment == "GEN_2" {
			gen = "Gen2"
		}
		rows[i] = table.Row{
			item.Name,
			item.Region,
			item.TriggerType,
			item.Runtime,
			gen,
			item.State,
		}
	}
	s.table.SetRows(rows)
}

// getFilteredFunctions returns filtered functions based on the query string
func (s *Service) getFilteredFunctions(functions []Function, query string) []Function {
	if query == "" {
		return functions
	}
	return components.FilterSlice(functions, query, func(fn Function, q string) bool {
		return components.ContainsMatch(fn.Name, fn.Region, fn.TriggerType, fn.Runtime, fn.State, fn.Environment)(q)
	})
}
