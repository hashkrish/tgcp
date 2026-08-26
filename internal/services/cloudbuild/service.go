package cloudbuild

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 30 * time.Second

// =============================================================================
// Models
// =============================================================================

// BuildItem represents a Cloud Build build
type BuildItem struct {
	ID           string
	Status       string
	StatusDetail string
	TriggerID    string
	CreateTime   time.Time
	StartTime    time.Time
	FinishTime   time.Time
	Duration     time.Duration
	LogURL       string
	Images       []string
}

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
)

// Message types for async operations
type dataMsg []BuildItem
type errMsg error

// =============================================================================
// Service Definition
// =============================================================================

// Service implements the services.Service interface
type Service struct {
	client    *Client
	projectID string

	// Dimensions (for custom layouts)
	width  int
	height int

	// UI Components
	table         *components.StandardTable
	filter        components.FilterModel
	filterSession components.FilterSession[BuildItem]
	spinner       components.SpinnerModel

	// Data State
	items  []BuildItem
	err    error
	loaded bool // Track if initial data has been loaded

	// View State
	viewState    ViewState
	selectedItem *BuildItem

	// Cache
	cache *core.Cache
}

// NewService creates a new instance of the service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "ID", Width: 14},
		{Title: "Status", Width: 12},
		{Title: "Trigger", Width: 14},
		{Title: "Created", Width: 19},
		{Title: "Duration", Width: 10},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter items..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
		loaded:    false,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredItems, svc.updateTable)
	return svc
}

// Name returns the full human-readable name
func (s *Service) Name() string {
	return "Cloud Build"
}

// ShortName returns the identifier used for routing (e.g., "gce", "sql")
func (s *Service) ShortName() string {
	return "cloudbuild"
}

// HelpText returns context-aware keybindings for the status bar
func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewList:
		return "r:Refresh  /:Filter  Enter:Detail"
	case ViewDetail:
		return "Esc/q:Back"
	default:
		return ""
	}
}

// =============================================================================
// Lifecycle & Interface Implementation
// =============================================================================

// InitService initializes the API client - called once when service is first accessed
func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx)
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

// Reinit reinitializes the service with a new project ID (on project switch)
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	s.loaded = false // Force reload on next entry
	return s.InitService(ctx, projectID)
}

// Init returns startup commands (background tick)
func (s *Service) Init() tea.Cmd {
	return s.tick()
}

// tick creates a background ticker for cache invalidation/refresh
func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Refresh triggers a forced data reload with spinner
func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""), // Empty string = playful random messages
		s.fetchDataCmd(true),
	)
}

// Reset clears the service state when navigating away
func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedItem = nil
	s.err = nil // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
}

// IsRootView returns true if at the top-level list (used for 'q' navigation)
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// Focus handles input focus - triggers initial load if needed
func (s *Service) Focus() {
	s.table.Focus()
}

// Blur handles loss of input focus
func (s *Service) Blur() {
	s.table.Blur()
}

// =============================================================================
// Update Loop
// =============================================================================

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchDataCmd(false), s.tick())

	case dataMsg:
		s.spinner.Stop()
		s.items = msg
		s.loaded = true
		s.filterSession.Apply(s.items)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

// handleKeyMsg processes keyboard input based on current view state
func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

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

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "enter":
			items := s.getCurrentItems()
			if idx := s.table.Cursor(); idx >= 0 && idx < len(items) {
				s.selectedItem = &items[idx]
				s.viewState = ViewDetail
			}
			return s, nil
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
			s.selectedItem = nil
			return s, nil
		}
	}

	return s, nil
}

// =============================================================================
// View Rendering
// =============================================================================

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Builds")
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
	if s.selectedItem == nil {
		return "Error: No item selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedItem.ID,
	)

	rows := []components.KeyValue{
		{Key: "ID", Value: s.selectedItem.ID},
		{Key: "Status", Value: s.selectedItem.Status},
	}
	if s.selectedItem.StatusDetail != "" {
		rows = append(rows, components.KeyValue{Key: "Status Detail", Value: s.selectedItem.StatusDetail})
	}
	if s.selectedItem.TriggerID != "" {
		rows = append(rows, components.KeyValue{Key: "Trigger ID", Value: s.selectedItem.TriggerID})
	}
	if !s.selectedItem.CreateTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Created", Value: s.selectedItem.CreateTime.Format("2006-01-02 15:04:05")})
	}
	if !s.selectedItem.StartTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Started", Value: s.selectedItem.StartTime.Format("2006-01-02 15:04:05")})
	}
	if !s.selectedItem.FinishTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Finished", Value: s.selectedItem.FinishTime.Format("2006-01-02 15:04:05")})
	}
	if s.selectedItem.Duration > 0 {
		rows = append(rows, components.KeyValue{Key: "Duration", Value: s.selectedItem.Duration.Round(time.Second).String()})
	}
	if len(s.selectedItem.Images) > 0 {
		rows = append(rows, components.KeyValue{Key: "Images", Value: strings.Join(s.selectedItem.Images, ", ")})
	}
	if s.selectedItem.LogURL != "" {
		rows = append(rows, components.KeyValue{Key: "Log URL", Value: s.selectedItem.LogURL})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Build Details",
		Rows:  rows,
	})

	actions := components.RenderFooterHint("q Back")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		actions,
	)
}

// =============================================================================
// Data Fetching
// =============================================================================

func (s *Service) fetchDataCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("cloudbuild_items:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if items, ok := val.([]BuildItem); ok {
					return dataMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		items, err := s.client.ListBuilds(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, items, CacheTTL)
		}

		return dataMsg(items)
	}
}

// =============================================================================
// Table Updates
// =============================================================================

func (s *Service) updateTable(items []BuildItem) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		created := ""
		if !item.CreateTime.IsZero() {
			created = item.CreateTime.Format("2006-01-02 15:04:05")
		}
		duration := ""
		if item.Duration > 0 {
			duration = item.Duration.Round(time.Second).String()
		}
		rows[i] = table.Row{
			shortID(item.ID),
			components.RenderStatus(item.Status),
			shortID(item.TriggerID),
			created,
			duration,
		}
	}
	s.table.SetRows(rows)
}

// shortID trims a UUID-style identifier down to a readable prefix for
// table display; the full value is still shown in the detail view.
func shortID(id string) string {
	const n = 12
	if len(id) <= n {
		return id
	}
	return id[:n]
}

func (s *Service) getCurrentItems() []BuildItem {
	return s.getFilteredItems(s.items, s.filter.Value())
}

func (s *Service) getFilteredItems(items []BuildItem, query string) []BuildItem {
	if query == "" {
		return items
	}
	return components.FilterSlice(items, query, func(item BuildItem, q string) bool {
		return components.ContainsMatch(item.ID, item.Status, item.TriggerID, item.StatusDetail)(q)
	})
}
