package parametermanager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second // Parameters rarely change

// -----------------------------------------------------------------------------
// Message Types
// -----------------------------------------------------------------------------

type tickMsg time.Time
type parametersMsg []Parameter
type versionsMsg []ParameterVersion
type valueMsg string
type renderedMsg string
type errMsg error

// ViewState defines the current UI state
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewVersions
	ViewVersionDetail
	ViewCreate
	ViewCreateVersion
	ViewUpdate
	ViewConfirmation
)

// newVersionCreateForm builds the FormModel for adding a new version to an
// existing parameter, matching `gcloud parametermanager parameters versions
// create --payload-data`.
func newVersionCreateForm() components.FormModel {
	return components.NewForm("New Parameter Version", []components.FormField{
		{Label: "Version ID", Placeholder: "v1", Required: true},
		{Label: "Payload", Placeholder: "the parameter value", Required: true},
	})
}

// newParameterUpdateForm builds the FormModel for updating a parameter's
// labels, seeded with its current labels rendered as comma-separated
// key=value pairs. Format is immutable after creation and versions
// create/render are out of scope for this minimal Update flow.
func newParameterUpdateForm(p Parameter) components.FormModel {
	var pairs []string
	for k, v := range p.Labels {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return components.NewForm("Update Parameter: "+p.Name, []components.FormField{
		{Label: "Labels (key=value,key2=value2)", Default: strings.Join(pairs, ",")},
	})
}

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

// Service implements the services.Service interface for Parameter Manager
type Service struct {
	client    *Client
	projectID string

	// Dimensions
	width  int
	height int

	// UI Components
	table         *components.StandardTable
	versionTable  *components.StandardTable
	filter        components.FilterModel
	filterSession components.FilterSession[Parameter]
	spinner       components.SpinnerModel

	// Data State
	parameters []Parameter
	versions   []ParameterVersion
	err        error

	// View State
	viewState         ViewState
	selectedParameter *Parameter
	selectedVersion   *ParameterVersion
	value             string
	valueErr          error

	createForm        components.FormModel
	versionCreateForm components.FormModel
	// rendered indicates s.value currently holds a *rendered* payload (from
	// RenderVersion, references expanded) rather than the raw stored one.
	rendered   bool
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// Cache
	cache *core.Cache
}

// NewService creates a new Parameter Manager service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Format", Width: 15},
		{Title: "Labels", Width: 25},
		{Title: "Created", Width: 20},
	}

	t := components.NewStandardTable(columns)

	versionColumns := []table.Column{
		{Title: "Version", Width: 10},
		{Title: "Disabled", Width: 12},
		{Title: "Created", Width: 25},
	}
	vt := components.NewStandardTable(versionColumns)

	svc := &Service{
		table:        t,
		versionTable: vt,
		filter:       components.NewFilterWithPlaceholder("Filter parameters..."),
		spinner:      components.NewSpinner(),
		viewState:    ViewList,
		cache:        cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredParameters, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Parameter Manager"
}

func (s *Service) ShortName() string {
	return "parametermanager"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewList:
		return "r:Refresh  /:Filter  n:New Parameter  Enter:Detail"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewDetail:
		return "v:Versions  u:Update  d:Delete  Esc/q:Back"
	case ViewVersions:
		return "n:New Version  Enter:View Version  Esc/q:Back to Detail"
	case ViewVersionDetail:
		return "R:Render  Esc/q:Back"
	case ViewCreate, ViewUpdate, ViewCreateVersion:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	default:
		return ""
	}
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
	return tea.Batch(s.spinner.Start(""), s.fetchParametersCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchParametersCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedParameter = nil
	s.selectedVersion = nil
	s.versions = nil
	s.value = ""
	s.valueErr = nil
	s.rendered = false
	s.err = nil
	s.table.SetCursor(0)
	s.versionTable.SetCursor(0)
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
		return s, tea.Batch(s.fetchParametersCmd(false), s.tick())

	case parametersMsg:
		s.spinner.Stop()
		s.parameters = msg
		s.filterSession.Apply(s.parameters)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case versionsMsg:
		s.spinner.Stop()
		s.versions = msg
		s.updateVersionTable(msg)
		return s, nil

	case valueMsg:
		s.spinner.Stop()
		s.value = string(msg)
		s.valueErr = nil
		s.rendered = false
		return s, nil

	case renderedMsg:
		s.spinner.Stop()
		s.value = string(msg)
		s.valueErr = nil
		s.rendered = true
		return s, nil

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
			s.selectedParameter = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.pendingAction == "create-version" {
			s.pendingAction = ""
			if msg.err != nil {
				s.versionCreateForm.SubmitErr = msg.err.Error()
				s.viewState = ViewCreateVersion
				return s, nil
			}
			s.viewState = ViewVersions
			if s.selectedParameter == nil {
				return s, nil
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.spinner.Start(""),
				s.fetchVersionsCmd(s.selectedParameter.FullName),
			)
		}
		if msg.err != nil {
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
		if s.viewState == ViewUpdate {
			s.viewState = ViewDetail
		} else {
			s.viewState = ViewList
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
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		s.versionTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		switch s.viewState {
		case ViewList:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		case ViewVersions:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.versionTable.Update(msg)
			s.versionTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch s.viewState {
	case ViewList:
		// Handle filter
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
		case "n":
			s.createForm = components.NewForm("New Parameter", []components.FormField{
				{Label: "Parameter ID", Placeholder: "my-parameter", Required: true},
				{Label: "Format", Default: "UNFORMATTED"},
			})
			s.viewState = ViewCreate
			return s, nil
		case "enter":
			params := s.getCurrentParameters()
			if idx := s.table.Cursor(); idx >= 0 && idx < len(params) {
				s.selectedParameter = &params[idx]
				s.viewState = ViewDetail
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.table.Update(msg)
		s.table = updatedTable
		return s, cmd

	case ViewCreate:
		result, formCmd := s.createForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewList
			return s, nil
		}
		if result.Submitted {
			return s, s.submitCreateCmd()
		}
		return s, formCmd

	case ViewUpdate:
		result, formCmd := s.updateForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewDetail
			return s, nil
		}
		if result.Submitted && s.selectedParameter != nil {
			return s, s.submitUpdateCmd(*s.selectedParameter)
		}
		return s, formCmd

	case ViewCreateVersion:
		result, formCmd := s.versionCreateForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewVersions
			return s, nil
		}
		if result.Submitted && s.selectedParameter != nil {
			s.pendingAction = "create-version"
			return s, s.submitCreateVersionCmd(*s.selectedParameter)
		}
		return s, formCmd

	case ViewDetail:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewList
			s.selectedParameter = nil
			s.versions = nil
			return s, nil
		case "u":
			if s.selectedParameter != nil {
				s.updateForm = newParameterUpdateForm(*s.selectedParameter)
				s.viewState = ViewUpdate
			}
			return s, nil
		case "v":
			// Fetch versions for selected parameter
			if s.selectedParameter != nil {
				s.viewState = ViewVersions
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchVersionsCmd(s.selectedParameter.FullName),
				)
			}
		case "d":
			if s.selectedParameter != nil {
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
			if s.pendingAction == "delete" && s.selectedParameter != nil {
				actionCmd = s.deleteParameterCmd(*s.selectedParameter)
			}
			s.viewState = s.actionSource
			return s, actionCmd
		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction = ""
			return s, nil
		}

	case ViewVersions:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewDetail
			return s, nil
		case "n":
			if s.selectedParameter != nil {
				s.versionCreateForm = newVersionCreateForm()
				s.viewState = ViewCreateVersion
			}
			return s, nil
		case "enter":
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.value = ""
				s.valueErr = nil
				s.viewState = ViewVersionDetail
				// Parameter Manager values are non-secret config, so unlike
				// Secret Manager there is no reveal-gating: fetch and show
				// the value as soon as the version is selected.
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchValueCmd(s.selectedVersion.FullName),
				)
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.versionTable.Update(msg)
		s.versionTable = updatedTable
		return s, cmd

	case ViewVersionDetail:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewVersions
			s.selectedVersion = nil
			s.value = ""
			s.valueErr = nil
			s.rendered = false
			return s, nil
		case "R":
			// Render: resolve any references (e.g. Secret Manager) in the
			// payload, matching `gcloud parametermanager parameters
			// versions render`.
			if s.selectedVersion != nil {
				return s, tea.Batch(s.spinner.Start(""), s.fetchRenderCmd(s.selectedVersion.FullName))
			}
			return s, nil
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Parameters")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewDetail:
		return s.renderDetailView()
	case ViewVersions:
		return s.renderVersionsView()
	case ViewVersionDetail:
		return s.renderVersionDetailView()
	case ViewCreate:
		return s.createForm.View()
	case ViewCreateVersion:
		return s.versionCreateForm.View()
	case ViewUpdate:
		return s.updateForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	default:
		return s.renderListView()
	}
}

// renderConfirmation renders the parameter-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedParameter == nil {
		return "Error: No parameter selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedParameter.Name,
		"parameter",
		fmt.Sprintf("Are you sure you want to DELETE parameter %s? This deletes every version of it.", s.selectedParameter.Name),
	)
}

func (s *Service) renderListView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
	)

	if len(s.parameters) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			breadcrumb,
			s.filter.View(),
			components.EmptyState("default"),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.filter.View(),
		s.table.View(),
	)
}

func (s *Service) renderDetailView() string {
	if s.selectedParameter == nil {
		return "No parameter selected"
	}

	p := s.selectedParameter

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		p.Name,
	)

	// Format labels
	labelStr := "-"
	if len(p.Labels) > 0 {
		var labels []string
		for k, v := range p.Labels {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		}
		labelStr = fmt.Sprintf("%v", labels)
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Parameter Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: p.Name},
			{Key: "Format", Value: p.Format},
			{Key: "Labels", Value: labelStr},
			{Key: "Created", Value: p.CreateTime.Local().Format("2006-01-02 15:04:05")},
			{Key: "Resource Name", Value: p.FullName},
		},
		FooterHint: "v Versions | q Back",
	})

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
	)
}

func (s *Service) renderVersionsView() string {
	if s.selectedParameter == nil {
		return "No parameter selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedParameter.Name,
		"Versions",
	)

	hint := styles.HelpStyle.Render("n New Version  |  enter View  |  q Back")
	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.versionTable.View(),
		"",
		hint,
	)
}

func (s *Service) renderVersionDetailView() string {
	if s.selectedParameter == nil || s.selectedVersion == nil {
		return "No version selected"
	}

	ver := s.selectedVersion

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedParameter.Name,
		"Versions",
		ver.Name,
	)

	rows := []components.KeyValue{
		{Key: "Version", Value: ver.Name},
		{Key: "Disabled", Value: fmt.Sprintf("%v", ver.Disabled)},
		{Key: "Created", Value: ver.CreateTime.Local().Format("2006-01-02 15:04:05")},
		{Key: "Resource Name", Value: ver.FullName},
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Parameter Version Details",
		Rows:       rows,
		FooterHint: "R Render (resolve references)  |  q Back",
	})

	var valueBlock string
	if s.valueErr != nil {
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorError).
			Render(fmt.Sprintf("Failed to load value: %v", s.valueErr))
	} else {
		labelText := "Value:"
		if s.rendered {
			labelText = "Value (rendered, references resolved):"
		}
		label := lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Render(labelText)
		value := lipgloss.NewStyle().
			Foreground(styles.ColorTextPrimary).
			Render(s.value)
		valueBlock = lipgloss.JoinVertical(lipgloss.Left, label, "", value)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		valueBlock,
	)
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	parameterID := s.createForm.Value("Parameter ID")
	format := s.createForm.Value("Format")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateParameter(s.projectID, parameterID, format); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Parameter %s created", parameterID)}
	}
}

// submitUpdateCmd fires the UpdateParameterLabels API call, parsing the
// update-form's comma-separated key=value pairs into a labels map.
func (s *Service) submitUpdateCmd(p Parameter) tea.Cmd {
	raw := s.updateForm.Value("Labels (key=value,key2=value2)")
	labels := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			labels[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateParameterLabels(p.FullName, labels); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating parameter %s...", p.Name)}
	}
}

// deleteParameterCmd triggers deletion of the given parameter
func (s *Service) deleteParameterCmd(p Parameter) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteParameter(p.FullName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting parameter %s...", p.Name)}
	}
}

// -----------------------------------------------------------------------------
// Data Fetching
// -----------------------------------------------------------------------------

func (s *Service) fetchParametersCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("parametermanager:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if params, ok := val.([]Parameter); ok {
					return parametersMsg(params)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		params, err := s.client.ListParameters(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, params, CacheTTL)
		}

		return parametersMsg(params)
	}
}

func (s *Service) fetchVersionsCmd(parameterName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		versions, err := s.client.ListVersions(parameterName)
		if err != nil {
			return errMsg(err)
		}

		return versionsMsg(versions)
	}
}

func (s *Service) fetchValueCmd(versionFullName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		value, err := s.client.GetVersionPayload(versionFullName)
		if err != nil {
			return errMsg(err)
		}

		return valueMsg(value)
	}
}

// fetchRenderCmd resolves a version's payload, expanding any references it
// contains, matching `gcloud parametermanager parameters versions render`.
func (s *Service) fetchRenderCmd(versionFullName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		rendered, err := s.client.RenderVersion(versionFullName)
		if err != nil {
			return errMsg(err)
		}
		return renderedMsg(rendered)
	}
}

// submitCreateVersionCmd fires the CreateVersion API call using the current
// version-create-form values.
func (s *Service) submitCreateVersionCmd(param Parameter) tea.Cmd {
	versionID := s.versionCreateForm.Value("Version ID")
	payload := s.versionCreateForm.Value("Payload")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateVersion(param.FullName, versionID, payload); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Created version %s on %s", versionID, param.Name)}
	}
}

// -----------------------------------------------------------------------------
// Table Updates
// -----------------------------------------------------------------------------

func (s *Service) updateTable(params []Parameter) {
	rows := make([]table.Row, len(params))
	for i, p := range params {
		// Format labels for display
		labelStr := "-"
		if len(p.Labels) > 0 {
			count := len(p.Labels)
			if count == 1 {
				for k, v := range p.Labels {
					labelStr = fmt.Sprintf("%s=%s", k, v)
				}
			} else {
				labelStr = fmt.Sprintf("%d labels", count)
			}
		}

		rows[i] = table.Row{
			p.Name,
			p.Format,
			labelStr,
			p.CreateTime.Local().Format("2006-01-02 15:04"),
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateVersionTable(versions []ParameterVersion) {
	rows := make([]table.Row, len(versions))
	for i, v := range versions {
		rows[i] = table.Row{
			v.Name,
			fmt.Sprintf("%v", v.Disabled),
			v.CreateTime.Local().Format("2006-01-02 15:04:05"),
		}
	}
	s.versionTable.SetRows(rows)
}

func (s *Service) getCurrentParameters() []Parameter {
	return s.getFilteredParameters(s.parameters, s.filter.Value())
}

func (s *Service) getFilteredParameters(params []Parameter, query string) []Parameter {
	if query == "" {
		return params
	}
	return components.FilterSlice(params, query, func(p Parameter, q string) bool {
		// Build label string for search
		var labelStr string
		for k, v := range p.Labels {
			labelStr += k + "=" + v + " "
		}
		return components.ContainsMatch(p.Name, p.Format, labelStr)(q)
	})
}
