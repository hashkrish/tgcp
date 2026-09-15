package cloudbuild

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/services/logging"
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
	// LoggingMode is Build.Options.Logging's raw enum string (e.g.
	// "STACKDRIVER_ONLY", "GCS_ONLY", "LEGACY", "NONE", or "" if unset).
	// STACKDRIVER_ONLY ("Cloud Logging only") -- the modern default -- means
	// there is no GCS log object at all: LogsBucket is empty, and "l"
	// (LogTail) falls back to the shared Cloud Logging view. See that
	// fallback's doc comment for why heavily \r-redrawn output (e.g. `docker
	// push` progress) is unrecoverably corrupted for such builds, by Cloud
	// Logging's own ingestion, not by anything in this app.
	LoggingMode   string
	Tags          []string
	Substitutions map[string]string
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
	ViewTriggers
	ViewTriggerDetail
	ViewTriggerEdit
	ViewTriggerCreate
	ViewWorkerPools
	ViewWorkerPoolCreate
	ViewConnections
	ViewCBRepositories
	ViewBuildLogs
)

// Message types for async operations
type dataMsg []BuildItem
type errMsg error

// buildLogTickMsg drives the build-log live-tail poll, separately from the
// main tickMsg (which refreshes the Builds list every CacheTTL=30s) -- a
// faster, dedicated cadence so a live tail actually feels live, matching the
// shared Cloud Logging view's own live-tail tick.
type buildLogTickMsg time.Time

const buildLogTickInterval = 3 * time.Second

func (s *Service) buildLogTick() tea.Cmd {
	return tea.Tick(buildLogTickInterval, func(t time.Time) tea.Msg { return buildLogTickMsg(t) })
}

// buildLogMsg carries a chunk of newly-read raw build-log text (see
// FetchBuildLogTail), or an error. text is "" and err is nil when a poll
// found nothing new since newOffset. buildID identifies which build the
// fetch was for, so a response that arrives after the user has switched to
// (or backed out to a different) build's log tail can be detected and
// dropped instead of being spliced into the wrong build's pane.
type buildLogMsg struct {
	buildID   string
	text      string
	newOffset int64
	err       error
}

// actionResultMsg carries the result of an async mutating action (e.g.
// build submission). resource/name/action identify what was acted on, for
// job-history recording.
type actionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
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
	pendingAction string    // "retry", "cancel", "run-trigger", "delete-trigger", "delete-worker-pool", "delete-connection", "delete-repository"
	actionSource  ViewState // Where to return after confirmation

	// Triggers sub-view state
	triggers        []TriggerItem
	triggersTable   *components.StandardTable
	selectedTrigger *TriggerItem
	triggerForm     components.FormModel

	// Worker Pools sub-view state (regional; wpRegion is set by the Create
	// form and reused for subsequent list refreshes)
	workerPools        []WorkerPoolItem
	workerPoolsTable   *components.StandardTable
	selectedWorkerPool *WorkerPoolItem
	workerPoolForm     components.FormModel
	wpRegion           string

	// Connections/Repositories (2nd-gen) sub-view state
	connections          []ConnectionItem
	connectionsTable     *components.StandardTable
	selectedConnection   *ConnectionItem
	connRegion           string
	cbRepositories       []CBRepositoryItem
	cbRepositoriesTable  *components.StandardTable
	selectedCBRepository *CBRepositoryItem

	// Build log tail sub-view state (raw GCS log object, see api.go's
	// FetchBuildLogTail) -- a scrolling text pane, not a table, since a raw
	// build log has no structured Time/Severity/Message columns.
	buildLogViewport viewport.Model
	buildLogBuildID  string
	buildLogBucket   string
	buildLogText     string
	buildLogOffset   int64
	buildLogLive     bool

	// Cache
	cache *core.Cache
}

// NewService creates a new instance of the service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "ID", Width: 14},
		{Title: "Status", Width: 14},
		{Title: "Trigger", Width: 14},
		{Title: "Created", Width: 19},
		{Title: "Duration", Width: 10},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:               t,
		filter:              components.NewFilterWithPlaceholder("Filter items..."),
		spinner:             components.NewSpinner(),
		viewState:           ViewList,
		cache:               cache,
		loaded:              false,
		triggersTable:       newTriggersTable(),
		workerPoolsTable:    newWorkerPoolsTable(),
		connectionsTable:    newConnectionsTable(),
		cbRepositoriesTable: newCBRepositoriesTable(),
		buildLogViewport:    viewport.New(80, 20),
		wpRegion:            "us-central1",
		connRegion:          "us-central1",
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
		return "r:Refresh  /:Filter  Enter:Detail  s:Submit Build  g:Triggers  p:Worker Pools  x:Connections"
	case ViewDetail:
		return "Esc/q:Back  t:Retry  c:Cancel  l:Log Tail"
	case ViewCreate, ViewTriggerCreate, ViewTriggerEdit, ViewWorkerPoolCreate:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewTriggers:
		return "Esc/q:Back  n:New  Enter:Detail  R:Run  d:Delete"
	case ViewTriggerDetail:
		return "Esc/q:Back  e:Edit  E:Enable/Disable  R:Run  d:Delete"
	case ViewWorkerPools:
		return "Esc/q:Back  n:New  d:Delete"
	case ViewConnections:
		return "Esc/q:Back  Enter:Repositories  d:Delete"
	case ViewCBRepositories:
		return "Esc/q:Back  d:Delete"
	case ViewBuildLogs:
		liveLabel := "L:Live Tail"
		if s.buildLogLive {
			liveLabel = "L:Stop Live Tail"
		}
		return fmt.Sprintf("Esc/q:Back  %s  ↑↓/PgUp/PgDn:Scroll", liveLabel)
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
	s.buildLogLive = false
	s.buildLogText = ""
	s.buildLogOffset = 0
}

// IsRootView returns true if at the top-level list (used for 'q' navigation)
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// SetActiveTab switches directly to one of Cloud Build's sub-views by string
// key, so the command palette can deep-link into it (e.g. "Triggers")
// instead of it only being reachable by first opening Builds and then
// pressing 'g'/'p'/'x' (see serviceSubTabs in internal/ui/model.go).
// Returns false for an unrecognized key, treated as a harmless no-op by
// callers. Mirrors the "g"/"p"/"x" key handlers in handleKeyMsg, including
// the lazy fetch each already does when its list hasn't been loaded yet.
func (s *Service) SetActiveTab(tab string) (bool, tea.Cmd) {
	switch tab {
	case "triggers":
		s.viewState = ViewTriggers
		return true, s.fetchTriggersCmd()
	case "worker-pools":
		s.viewState = ViewWorkerPools
		return true, s.fetchWorkerPoolsCmd()
	case "connections":
		s.viewState = ViewConnections
		return true, s.fetchConnectionsCmd()
	default:
		return false, nil
	}
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

	case buildLogTickMsg:
		if s.viewState == ViewBuildLogs && s.buildLogLive {
			return s, tea.Batch(s.fetchBuildLogCmd(), s.buildLogTick())
		}
		return s, nil

	case buildLogMsg:
		if msg.buildID != s.buildLogBuildID {
			// Stale response for a build the user has since navigated away
			// from (e.g. backed out and opened a different build's log tail
			// before this fetch returned) -- drop it rather than splicing
			// its text/offset into whatever's now selected.
			return s, nil
		}
		if msg.err != nil {
			s.err = msg.err
			return s, nil
		}
		s.err = nil // Clear any earlier transient poll error now that one succeeded.
		if msg.text != "" {
			s.buildLogText += msg.text
			s.buildLogOffset = msg.newOffset
			s.buildLogViewport.SetContent(logging.StripTerminalControlChars(s.buildLogText))
			s.buildLogViewport.GotoBottom()
		}
		return s, nil

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

	case triggersMsg, workerPoolsMsg, connectionsMsg, cbRepositoriesMsg:
		model, rcmd, _ := s.handleResourceMsg(msg)
		return model, rcmd

	case resourceActionResultMsg:
		model, rcmd, _ := s.handleResourceMsg(msg)
		if msg.err != nil {
			return model, tea.Batch(rcmd, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			})
		}
		if msg.msg != "" {
			return model, tea.Batch(rcmd, func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			})
		}
		return model, rcmd

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		s.triggersTable.HandleWindowSizeDefault(msg)
		s.workerPoolsTable.HandleWindowSizeDefault(msg)
		s.connectionsTable.HandleWindowSizeDefault(msg)
		s.cbRepositoriesTable.HandleWindowSizeDefault(msg)
		s.buildLogViewport.Width = msg.Width - 4
		s.buildLogViewport.Height = msg.Height - 8

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

	if model, rcmd, handled := s.handleResourceKeyMsg(msg); handled {
		return model, rcmd
	}

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
		case "g": // Triggers
			s.viewState = ViewTriggers
			return s, s.fetchTriggersCmd()
		case "p": // Worker Pools
			s.viewState = ViewWorkerPools
			return s, s.fetchWorkerPoolsCmd()
		case "x": // Connections (2nd-gen)
			s.viewState = ViewConnections
			return s, s.fetchConnectionsCmd()
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
		case "l":
			if s.selectedItem == nil {
				return s, nil
			}
			if s.selectedItem.LogsBucket == "" {
				// No GCS logs bucket on this build (e.g. a CLOUD_LOGGING_ONLY
				// build) -- fall back to the shared Cloud Logging view
				// filtered to this build's ID, in live-tail mode whenever the
				// build is still in progress.
				filter := fmt.Sprintf(`resource.type="build" AND resource.labels.build_id="%s"`, s.selectedItem.ID)
				heading := fmt.Sprintf("Build: %s", s.selectedItem.ID)
				live := isBuildInProgress(s.selectedItem.Status)
				return s, func() tea.Msg {
					return core.SwitchToLogsMsg{Filter: filter, Source: "cloudbuild", Heading: heading, Live: live}
				}
			}
			// Read the build's raw GCS log object directly. Cloud Logging's
			// own ingestion of TTY-progress-heavy build step output (e.g.
			// `docker push`) has already collapsed "\r"-redrawn lines into
			// truncated fragments by the time it reaches the Logging API --
			// the raw GCS object is the same unmangled byte stream
			// `gcloud builds log` itself reads.
			s.buildLogBuildID = s.selectedItem.ID
			s.buildLogBucket = s.selectedItem.LogsBucket
			s.buildLogText = ""
			s.buildLogOffset = 0
			s.buildLogLive = isBuildInProgress(s.selectedItem.Status)
			s.buildLogViewport.SetContent("")
			s.viewState = ViewBuildLogs
			if s.buildLogLive {
				return s, tea.Batch(s.fetchBuildLogCmd(), s.buildLogTick())
			}
			return s, s.fetchBuildLogCmd()
		}
	}

	if s.viewState == ViewBuildLogs {
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewDetail
			s.buildLogLive = false
			return s, nil
		case "L":
			s.buildLogLive = !s.buildLogLive
			if s.buildLogLive {
				return s, tea.Batch(s.fetchBuildLogCmd(), s.buildLogTick())
			}
			return s, nil
		}
		var vp viewport.Model
		vp, cmd = s.buildLogViewport.Update(msg)
		s.buildLogViewport = vp
		return s, cmd
	}

	if s.viewState == ViewConfirmation {
		switch msg.String() {
		case "y", "enter":
			if rcmd, handled := s.runResourceConfirmedAction(); handled {
				s.pendingAction = ""
				return s, rcmd
			}
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
	if s.viewState == ViewBuildLogs {
		return s.renderBuildLogView()
	}
	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if resourceView := s.renderResourceView(); resourceView != "" {
		return resourceView
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
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "build", Name: opts.ImageName, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateBuild(s.projectID, opts)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "build", name: opts.ImageName, action: "create"}
		}
		return actionResultMsg{msg: "Submitting build...", resource: "build", name: opts.ImageName, action: "create"}
	}
}

func (s *Service) renderConfirmation() string {
	if view, handled := s.renderResourceConfirmation(); handled {
		return view
	}
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
	logStorage := describeLoggingMode(s.selectedItem.LoggingMode)
	if s.selectedItem.LogsBucket == "" {
		logStorage += " (no raw GCS log file -- \"l\" falls back to Cloud Logging, which may show corrupted TTY-progress lines for tools like `docker push`)"
	}
	rows = append(rows, components.KeyValue{Key: "Log Storage", Value: logStorage})
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

// renderBuildLogView renders the raw build-log tail as a scrolling text pane.
func (s *Service) renderBuildLogView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.buildLogBuildID,
		"Log Tail",
	)

	liveIndicator := ""
	if s.buildLogLive {
		liveStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
		liveIndicator = "  " + liveStyle.Render("● LIVE")
	}

	content := s.buildLogViewport.View()
	if s.buildLogText == "" {
		content = components.EmptyState("logs")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb+liveIndicator,
		"",
		content,
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
			item.Status,
			shortID(item.TriggerID),
			created,
			duration,
		}
	}
	s.table.SetRows(rows)
}

// isBuildInProgress reports whether status is one of Cloud Build's
// non-terminal Build.Status enum values, i.e. still producing log lines.
func isBuildInProgress(status string) bool {
	switch status {
	case "QUEUED", "WORKING", "PENDING":
		return true
	default:
		return false
	}
}

// describeLoggingMode renders Build.Options.Logging's raw enum string as a
// human-readable description, and notes when there's no GCS log object to
// read a raw log tail from ("l") -- see BuildItem.LoggingMode's doc comment.
func describeLoggingMode(mode string) string {
	switch mode {
	case "STACKDRIVER_ONLY":
		return "Cloud Logging only"
	case "LOGGING_UNSPECIFIED", "":
		return "Default"
	case "GCS_ONLY":
		return "GCS only"
	case "LEGACY":
		return "Legacy (Cloud Logging + GCS)"
	case "NONE":
		return "Disabled"
	default:
		return mode
	}
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
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "build", Name: shortID(item.ID), Action: "retry",
		}, func() error {
			return s.client.RetryBuild(s.projectID, item.ID)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "build", name: shortID(item.ID), action: "retry"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Retrying build %s...", shortID(item.ID)), resource: "build", name: shortID(item.ID), action: "retry"}
	}
}

func (s *Service) cancelBuildCmd(item BuildItem) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(),
			Resource: "build", Name: shortID(item.ID), Action: "cancel",
		}, func() error {
			return s.client.CancelBuild(s.projectID, item.ID)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "build", name: shortID(item.ID), action: "cancel"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Cancelling build %s...", shortID(item.ID)), resource: "build", name: shortID(item.ID), action: "cancel"}
	}
}

// fetchBuildLogCmd reads the next unread chunk of the current build's raw
// GCS log object (see Client.FetchBuildLogTail), starting from
// buildLogOffset.
func (s *Service) fetchBuildLogCmd() tea.Cmd {
	bucket := s.buildLogBucket
	buildID := s.buildLogBuildID
	offset := s.buildLogOffset
	return func() tea.Msg {
		if s.client == nil {
			return buildLogMsg{buildID: buildID, err: fmt.Errorf("client not initialized")}
		}
		text, newOffset, err := s.client.FetchBuildLogTail(context.Background(), bucket, buildID, offset)
		if err != nil {
			return buildLogMsg{buildID: buildID, err: err}
		}
		return buildLogMsg{buildID: buildID, text: text, newOffset: newOffset}
	}
}
