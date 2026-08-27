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

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
)

type functionsMsg []Function
type errMsg error

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
		return "r:Refresh  /:Filter  Ent:Detail"
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

	return s.renderListView()
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

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

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
