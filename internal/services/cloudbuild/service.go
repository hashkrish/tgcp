package cloudbuild

import (
	"context"
	"fmt"
	"sort"
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
	ID             string
	Status         string
	StatusDetail   string
	TriggerID      string
	CreateTime     time.Time
	StartTime      time.Time
	FinishTime     time.Time
	Duration       time.Duration
	LogURL         string
	Images         []string
	Source         string
	ServiceAccount string
	LogsBucket     string
	Tags           []string
	Substitutions  map[string]string
}

// formatSubstitutions renders a build's substitution variables as "key=value" pairs.
func formatSubstitutions(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// BuildCreateOpts holds the minimal set of fields needed to submit a new
// build via the Create form.
type BuildCreateOpts struct {
	StepImage     string // build step's container image, e.g. gcr.io/cloud-builders/docker
	StepArgs      string // comma-separated args passed to the step
	ImageName     string // optional image to record as produced by the build
	Substitutions string // optional "KEY=value,KEY2=value2"
}

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewConfirmation
)

// Message types for async operations
type dataMsg []BuildItem
type errMsg error
// actionResultMsg carries the result of an async mutating action (e.g.
// build submission).
type actionResultMsg struct {
	err error
	msg string
}

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

	createForm components.FormModel

	// Confirmation State
	pendingAction string    // "retry" or "cancel"
	actionSource  ViewState // Where to return after confirmation

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
		return "r:Refresh  /:Filter  Enter:Detail  s:Submit Build"
	case ViewDetail:
		return "Esc/q:Back  t:Retry  c:Cancel"
	case ViewCreate:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
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

	if s.viewState == ViewCreate {
		result, formCmd := s.createForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewList
			return s, nil
		}
		if result.Submitted {
			return s, s.submitBuildCmd()
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

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "s":
			s.createForm = components.NewForm("Submit Build", []components.FormField{
				{Label: "Step Image", Default: "gcr.io/cloud-builders/docker", Required: true},
				{Label: "Step Args", Default: "build,-t,gcr.io/PROJECT/IMAGE,.", Required: true},
				{Label: "Image Name"},
				{Label: "Substitutions"},
			})
			s.viewState = ViewCreate
			return s, nil
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
		case "t":
			if s.selectedItem != nil {
				s.pendingAction = "retry"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
			}
		case "c":
			if s.selectedItem != nil {
				s.pendingAction = "cancel"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
			}
		}
	}

	if s.viewState == ViewConfirmation {
		switch msg.String() {
		case "y", "enter":
			var actionCmd tea.Cmd
			switch s.pendingAction {
			case "retry":
				actionCmd = s.retryBuildCmd(*s.selectedItem)
			case "cancel":
				actionCmd = s.cancelBuildCmd(*s.selectedItem)
			}
			s.viewState = s.actionSource
			s.pendingAction = ""
			return s, actionCmd

		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction = ""
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
	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	return s.renderListView()
}

// submitBuildCmd fires the CreateBuild API call using the current
// createForm values.
func (s *Service) submitBuildCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := BuildCreateOpts{
		StepImage:     v["Step Image"],
		StepArgs:      v["Step Args"],
		ImageName:     v["Image Name"],
		Substitutions: v["Substitutions"],
	}
	s.viewState = ViewList
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateBuild(s.projectID, opts); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: "Submitting build..."}
	}
}

func (s *Service) renderConfirmation() string {
	if s.selectedItem == nil {
		return "Error: No build selected"
	}
	return components.RenderConfirmation(s.pendingAction, shortID(s.selectedItem.ID), "build")
}

func (s *Service) renderListView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
	)

	content := s.table.View()
	if len(s.items) == 0 {
		content = components.EmptyState("builds")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.filter.View(),
		content,
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
		{Key: "Status", Value: components.RenderStatus(s.selectedItem.Status)},
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
	if s.selectedItem.Source != "" {
		rows = append(rows, components.KeyValue{Key: "Source", Value: s.selectedItem.Source})
	}
	if s.selectedItem.ServiceAccount != "" {
		rows = append(rows, components.KeyValue{Key: "Service Account", Value: s.selectedItem.ServiceAccount})
	}
	if s.selectedItem.LogsBucket != "" {
		rows = append(rows, components.KeyValue{Key: "Logs Bucket", Value: s.selectedItem.LogsBucket})
	}
	if len(s.selectedItem.Tags) > 0 {
		rows = append(rows, components.KeyValue{Key: "Tags", Value: strings.Join(s.selectedItem.Tags, ", ")})
	}
	if len(s.selectedItem.Substitutions) > 0 {
		rows = append(rows, components.KeyValue{Key: "Substitutions", Value: formatSubstitutions(s.selectedItem.Substitutions)})
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

func (s *Service) retryBuildCmd(item BuildItem) tea.Cmd {
	return func() tea.Msg {
		err := s.client.RetryBuild(s.projectID, item.ID)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Retrying build %s...", shortID(item.ID))}
	}
}

func (s *Service) cancelBuildCmd(item BuildItem) tea.Cmd {
	return func() tea.Msg {
		err := s.client.CancelBuild(s.projectID, item.ID)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Cancelling build %s...", shortID(item.ID))}
	}
}
