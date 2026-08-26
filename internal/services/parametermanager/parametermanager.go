package parametermanager

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

const CacheTTL = 60 * time.Second // Parameters rarely change

// -----------------------------------------------------------------------------
// Message Types
// -----------------------------------------------------------------------------

type tickMsg time.Time
type parametersMsg []Parameter
type versionsMsg []ParameterVersion
type valueMsg string
type errMsg error

// ViewState defines the current UI state
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewVersions
	ViewVersionDetail
)

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
		return "r:Refresh  /:Filter  Enter:Detail"
	case ViewDetail:
		return "v:Versions  Esc/q:Back"
	case ViewVersions:
		return "Enter:View Version  Esc/q:Back to Detail"
	case ViewVersionDetail:
		return "Esc/q:Back"
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
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		s.versionTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		} else if s.viewState == ViewVersions {
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

	case ViewDetail:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewList
			s.selectedParameter = nil
			s.versions = nil
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
		}

	case ViewVersions:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewDetail
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
	default:
		return s.renderListView()
	}
}

func (s *Service) renderListView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
	)

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

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.versionTable.View(),
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
		FooterHint: "q Back",
	})

	var valueBlock string
	if s.valueErr != nil {
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorError).
			Render(fmt.Sprintf("Failed to load value: %v", s.valueErr))
	} else {
		label := lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Render("Value:")
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
