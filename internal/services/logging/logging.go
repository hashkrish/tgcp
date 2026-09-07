package logging

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 10 * time.Second // Logs change frequently

// maxLiveBufferEntries caps how many entries a live tail keeps in memory --
// older entries are dropped from the front once exceeded, so a long-running
// build's log can't grow unbounded.
const maxLiveBufferEntries = 2000

// defaultPageSize is how many entries a single (non-live) page fetch pulls.
const defaultPageSize = 200

// Tick message for background refresh
type tickMsg time.Time

// Service implements the services.Service interface for Cloud Logging
type Service struct {
	client    *Client
	projectID string

	// UI Components
	table   *components.StandardTable
	spinner components.SpinnerModel

	// State
	entries       []LogEntry
	err           error
	nextPageToken string
	currentToken  string   // Token used for current page
	tokenStack    []string // History of tokens for "Previous" function

	// Dimensions
	width  int
	height int

	// Cache
	cache *core.Cache

	// Navigation
	returnTo string
	heading  string

	// baseFilter is the resource-scoping filter set externally via SetFilter
	// (e.g. by SwitchToLogsMsg from GCE/Cloud Run/Cloud SQL/GKE/Cloud
	// Build). Read-only from the user's perspective inside the log viewer.
	baseFilter string

	// userQuery is the free-text LQL the user types in the log viewer
	// itself (activated with "/"), ANDed onto baseFilter by composeFilter.
	userQuery string

	// queryFilter drives the "/"-activated text-entry UX for userQuery,
	// reusing components.FilterModel's textinput/styling only -- its
	// client-side FilterSlice semantics are NOT used here; submitting
	// (Enter) triggers a refetch via composeFilter, not local row
	// filtering.
	queryFilter components.FilterModel

	// severityThreshold is the minimum severity to show ("" or "DEFAULT"
	// means no filtering), chosen via the "s"-triggered severitySelect
	// picker below.
	severityThreshold string

	// selectingSeverity/severitySelect back the "s" key's severity-level
	// picker -- a modal list (see components.SelectModel) rather than a
	// one-at-a-time cycle, so jumping straight to e.g. EMERGENCY doesn't
	// require stepping through every level in between.
	selectingSeverity bool
	severitySelect    components.SelectModel

	// Live-tail mode (see SetLive): while true, the background tick polls
	// for and appends newly-arrived entries (oldest-first) instead of
	// replacing page 1 with a fresh newest-first fetch. lastEntryTime is the
	// timestamp of the most recently appended entry, used as the polling
	// cursor ("fetch entries at or after this"). lastEntryInsertIDs holds the
	// InsertIDs of every already-appended entry exactly at lastEntryTime, so
	// a ">="-based poll (inclusive, to avoid silently skipping entries that
	// share that exact timestamp) can dedup instead of re-appending them.
	live               bool
	lastEntryTime      time.Time
	lastEntryInsertIDs map[string]bool

	// Detail view -- a scrolling text pane (its content can easily exceed
	// one screen, especially a pretty-printed JSON payload), not a static
	// render.
	selectedEntry  *LogEntry
	viewingDetail  bool
	detailViewport viewport.Model

	// Resources mode: a secondary browser (toggled with "R") for the
	// project-scoped configuration resources this service manages besides
	// log entries themselves -- sinks, logs-based metrics, buckets, and
	// views. Kept fully separate from the log-entries table/paging state
	// above so the primary "jump to logs from another service" flow
	// (returnTo/heading/filter) is never disturbed by it.
	resourcesMode bool
	resourceTab   ResourceTab

	sinks   []Sink
	metrics []LogMetric
	buckets []LogBucket
	views   []LogView

	sinkTable   *components.StandardTable
	metricTable *components.StandardTable
	bucketTable *components.StandardTable
	viewTable   *components.StandardTable

	resourceCreateForm components.FormModel
	resourceViewState  ResourceViewState

	// pendingResourceDelete stages the resource-mode delete confirmation
	// dialog's target name.
	pendingResourceDelete string

	// viewBucketID/viewLocation scope the Views tab to a single log bucket.
	// Views are nested under a bucket in the real API; rather than build a
	// bucket-picker UI, this defaults to the project's default bucket
	// ("_Default" in "global") and can be changed with a small form.
	viewBucketID string
	viewLocation string
}

// ResourceTab selects which resource-mode table (sinks/metrics/buckets/views)
// is currently active.
type ResourceTab int

const (
	ResourceTabSinks ResourceTab = iota
	ResourceTabMetrics
	ResourceTabBuckets
	ResourceTabViews
)

var resourceTabOrder = []ResourceTab{ResourceTabSinks, ResourceTabMetrics, ResourceTabBuckets, ResourceTabViews}

func nextResourceTab(t ResourceTab) ResourceTab {
	for i, cur := range resourceTabOrder {
		if cur == t {
			return resourceTabOrder[(i+1)%len(resourceTabOrder)]
		}
	}
	return resourceTabOrder[0]
}

func prevResourceTab(t ResourceTab) ResourceTab {
	for i, cur := range resourceTabOrder {
		if cur == t {
			return resourceTabOrder[(i-1+len(resourceTabOrder))%len(resourceTabOrder)]
		}
	}
	return resourceTabOrder[0]
}

// ResourceViewState tracks the resource-mode sub-view, independent of the
// log-entries viewingDetail bool.
type ResourceViewState int

const (
	ResourceViewList ResourceViewState = iota
	ResourceViewCreate
	ResourceViewConfirmation
	ResourceViewGrantIAM
)

type sinksMsg []Sink
type metricsMsg []LogMetric
type bucketsMsg []LogBucket
type viewsMsg []LogView
type resourceActionResultMsg struct {
	err    error
	msg    string
	action string // e.g. "create", "delete", "grant" -- for job-history recording
	name   string // resource name/ID -- for job-history recording
}

func NewService(cache *core.Cache) *Service {
	// Compact table columns for log entries
	columns := []table.Column{
		{Title: "Time", Width: 19},
		{Title: "Sev", Width: 8},
		{Title: "Message", Width: 80},
	}

	t := components.NewStandardTable(columns)

	sinkTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 24},
		{Title: "Destination", Width: 50},
		{Title: "Disabled", Width: 10},
	})
	metricTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 24},
		{Title: "Description", Width: 40},
		{Title: "Filter", Width: 30},
	})
	bucketTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 20},
		{Title: "Retention (days)", Width: 18},
		{Title: "Locked", Width: 10},
	})
	viewTable := components.NewStandardTable([]table.Column{
		{Title: "Name", Width: 24},
		{Title: "Filter", Width: 50},
	})

	s := &Service{
		table:          t,
		spinner:        components.NewSpinner(),
		cache:          cache,
		sinkTable:      sinkTable,
		metricTable:    metricTable,
		bucketTable:    bucketTable,
		viewTable:      viewTable,
		viewBucketID:   "_Default",
		viewLocation:   "global",
		detailViewport: viewport.New(80, 20),
		queryFilter:    components.NewFilterWithPlaceholder(`LQL query, e.g. severity>=ERROR AND textPayload:"timeout"`),
	}
	s.queryFilter.TextInput.ShowSuggestions = true
	s.queryFilter.TextInput.CompletionStyle = lipgloss.NewStyle().Foreground(styles.ColorTextMuted)
	s.queryFilter.TextInput.SetSuggestions(lqlSuggestions())
	return s
}

// lqlSuggestions seeds the query input's built-in ghost-text autocomplete
// (bubbles/textinput's ShowSuggestions: type a prefix, see the rest greyed
// out inline, Tab to accept, Up/Down to cycle other matches) with commonly
// used Cloud Logging LQL fields and values, so the user rarely has to type
// LQL syntax from memory. Suggestions are matched against the *whole*
// current input as a prefix (see bubbles/textinput), so this only speeds up
// the clause currently being typed -- not clauses after an already-typed
// "AND " -- which is still the common case (starting a new query, or a
// severity threshold) worth optimizing for.
func lqlSuggestions() []string {
	sugs := []string{
		`resource.type="cloud_run_revision"`,
		`resource.type="gce_instance"`,
		`resource.type="gae_app"`,
		`resource.type="k8s_container"`,
		`resource.type="cloudsql_database"`,
		`resource.type="cloud_function"`,
		`resource.type="build"`,
		`resource.labels.service_name=`,
		`resource.labels.instance_id=`,
		`resource.labels.namespace_name=`,
		`resource.labels.container_name=`,
	}
	for _, lvl := range SeverityLevels {
		if lvl == "DEFAULT" {
			continue
		}
		sugs = append(sugs, fmt.Sprintf("severity>=%s", lvl))
	}
	sugs = append(sugs,
		`logName=`,
		`textPayload:`,
		`jsonPayload.message:`,
		`jsonPayload.`,
		`labels.`,
		`trace=`,
		`insertId=`,
		`httpRequest.status=`,
		`protoPayload.methodName=`,
		`timestamp>=`,
		`timestamp<=`,
	)
	return sugs
}

func (s *Service) Name() string {
	return "Cloud Logging"
}

func (s *Service) ShortName() string {
	return "logs"
}

func (s *Service) HelpText() string {
	if s.resourcesMode {
		switch s.resourceViewState {
		case ResourceViewCreate:
			return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
		case ResourceViewConfirmation:
			return "y:Confirm  n:Cancel"
		case ResourceViewGrantIAM:
			return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
		}
		hint := "[]:Switch Tab  R:Back to Logs  r:Refresh  n:New  d:Delete"
		if s.resourceTab == ResourceTabViews {
			hint += "  g:Grant IAM"
		}
		return hint
	}
	if s.viewingDetail {
		return "Esc/q:Back  ↑↓/PgUp/PgDn:Scroll"
	}
	if s.queryFilter.Active {
		return "Enter:Apply  Tab:Complete  ↑↓:Cycle Suggestion  Esc:Cancel"
	}
	if s.selectingSeverity {
		return "↑↓:Move  Enter:Select  Esc:Cancel"
	}
	liveLabel := "L:Live Tail"
	if s.live {
		liveLabel = "L:Stop Live Tail"
	}
	sevLabel := "Any"
	if s.severityThreshold != "" && s.severityThreshold != "DEFAULT" {
		sevLabel = s.severityThreshold
	}
	base := fmt.Sprintf("r:Refresh  %s  /:Query  s:Severity(%s)  Enter:Detail  R:Resources", liveLabel, sevLabel)
	if s.returnTo != "" {
		base = fmt.Sprintf("Esc:Back to %s  r:Refresh  %s  /:Query  s:Severity(%s)  Enter:Detail  R:Resources", s.returnTo, liveLabel, sevLabel)
	} else {
		base += "  Esc/q:Back"
	}
	if !s.live && len(s.tokenStack) > 0 {
		base += "  n:Newer"
	}
	if !s.live && s.nextPageToken != "" {
		base += "  p:Older"
	}
	return base
}

// Focus handles input focus
func (s *Service) Focus() {
	s.table.Focus()
	s.sinkTable.Focus()
	s.metricTable.Focus()
	s.bucketTable.Focus()
	s.viewTable.Focus()
}

// Blur handles loss of input focus
func (s *Service) Blur() {
	s.table.Blur()
	s.sinkTable.Blur()
	s.metricTable.Blur()
	s.bucketTable.Blur()
	s.viewTable.Blur()
}

// activeResourceTable returns the StandardTable backing the currently
// active resource tab.
func (s *Service) activeResourceTable() *components.StandardTable {
	switch s.resourceTab {
	case ResourceTabMetrics:
		return s.metricTable
	case ResourceTabBuckets:
		return s.bucketTable
	case ResourceTabViews:
		return s.viewTable
	default:
		return s.sinkTable
	}
}

func (s *Service) setActiveResourceTable(t *components.StandardTable) {
	switch s.resourceTab {
	case ResourceTabMetrics:
		s.metricTable = t
	case ResourceTabBuckets:
		s.bucketTable = t
	case ResourceTabViews:
		s.viewTable = t
	default:
		s.sinkTable = t
	}
}

// Msg types
type entriesMsg struct {
	entries   []LogEntry
	nextToken string
}

// liveEntriesMsg carries a live-tail poll's result. filter is the filter the
// poll was issued for, so a response that arrives after the user has since
// jumped to a different filter (e.g. a different build's log tail) can be
// detected and dropped instead of being mixed into the new filter's entries.
type liveEntriesMsg struct {
	filter  string
	entries []LogEntry
}
type errMsg error

// InitService initializes the service logic (API clients)
func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx, projectID)
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

// Init satisfies tea.Model interface, starts background tick
func (s *Service) Init() tea.Cmd {
	return tea.Batch(
		s.tick(),
		s.Refresh(), // Trigger first fetch
	)
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages specific to Logging
func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		if s.live {
			// Never auto-poll while inspecting a single entry's detail, so a
			// background tick doesn't yank it out from under the user.
			if s.viewingDetail {
				return s, s.tick()
			}
			return s, tea.Batch(s.fetchLiveEntriesCmd(), s.tick())
		}
		// Only auto-refresh when on page 1 and not inspecting a single entry's
		// detail, so a background tick never yanks the user's paging position
		// or the list backing an open detail view out from under them.
		if len(s.tokenStack) > 0 || s.currentToken != "" || s.viewingDetail {
			return s, s.tick()
		}
		return s, tea.Batch(s.fetchEntriesCmd(""), s.tick())

	case entriesMsg:
		s.spinner.Stop()
		s.entries = msg.entries
		s.nextPageToken = msg.nextToken
		s.updateTable()
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case liveEntriesMsg:
		s.spinner.Stop()
		if msg.filter != s.composeFilter() {
			// Stale response for a filter the user has since navigated away
			// from (e.g. jumped to a different build's log tail) -- drop it
			// rather than mixing its entries into the new filter's tail.
			return s, nil
		}
		if len(msg.entries) > 0 {
			// The poll is inclusive of lastEntryTime (">=", not ">") so ties
			// at that exact timestamp aren't silently skipped -- dedup
			// against what was already appended for it.
			fresh := make([]LogEntry, 0, len(msg.entries))
			for _, e := range msg.entries {
				if e.Timestamp.Equal(s.lastEntryTime) && s.lastEntryInsertIDs[e.InsertID] {
					continue
				}
				fresh = append(fresh, e)
			}
			if len(fresh) > 0 {
				s.entries = append(s.entries, fresh...)
				if excess := len(s.entries) - maxLiveBufferEntries; excess > 0 {
					s.entries = s.entries[excess:]
				}
				newMax := s.lastEntryTime
				for _, e := range fresh {
					if e.Timestamp.After(newMax) {
						newMax = e.Timestamp
					}
				}
				if newMax.After(s.lastEntryTime) {
					s.lastEntryTime = newMax
					s.lastEntryInsertIDs = make(map[string]bool)
				}
				for _, e := range fresh {
					if e.Timestamp.Equal(s.lastEntryTime) {
						s.lastEntryInsertIDs[e.InsertID] = true
					}
				}
				s.updateTable()
				s.table.GotoBottom()
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case sinksMsg:
		s.spinner.Stop()
		s.sinks = msg
		s.updateSinkTable()
		return s, nil

	case metricsMsg:
		s.spinner.Stop()
		s.metrics = msg
		s.updateMetricTable()
		return s, nil

	case bucketsMsg:
		s.spinner.Stop()
		s.buckets = msg
		s.updateBucketTable()
		return s, nil

	case viewsMsg:
		s.spinner.Stop()
		s.views = msg
		s.updateViewTable()
		return s, nil

	case resourceActionResultMsg:
		s.spinner.Stop()
		if msg.err != nil {
			core.RecordJob(core.Job{ProjectID: s.projectID, Service: s.ShortName(), Resource: "resource", Name: msg.name, Action: msg.action, Status: core.JobFailed, Error: msg.err.Error()})
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError} }
		}
		core.RecordJob(core.Job{ProjectID: s.projectID, Service: s.ShortName(), Resource: "resource", Name: msg.name, Action: msg.action, Status: core.JobSuccess})
		return s, tea.Batch(
			func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
			s.fetchResourceTabCmd(s.resourceTab),
		)

	case errMsg:
		s.spinner.Stop()
		s.err = msg

	case tea.WindowSizeMsg:
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		// Adjust message column to fill available width
		s.adjustTableColumns()
		s.detailViewport.Width = msg.Width - 4
		s.detailViewport.Height = msg.Height - 4

	case tea.MouseMsg:
		// Forward mouse events to table for click selection
		if s.resourcesMode {
			if s.resourceViewState == ResourceViewList {
				updatedTable, mcmd := s.activeResourceTable().Update(msg)
				s.setActiveResourceTable(updatedTable)
				return s, mcmd
			}
			return s, nil
		}
		if !s.viewingDetail {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.resourcesMode {
			return s.updateResourcesMode(msg)
		}

		if s.viewingDetail {
			switch msg.String() {
			case "esc", "q":
				s.viewingDetail = false
				s.selectedEntry = nil
				return s, nil
			}
			var vp viewport.Model
			vp, cmd = s.detailViewport.Update(msg)
			s.detailViewport = vp
			return s, cmd
		}

		if s.queryFilter.Active {
			switch msg.String() {
			case "enter":
				s.userQuery = s.queryFilter.TextInput.Value()
				s.queryFilter.ExitFilterMode()
				return s, s.Refresh()
			case "esc":
				// Cancel edit without discarding the previously applied query.
				s.queryFilter.TextInput.SetValue(s.userQuery)
				s.queryFilter.ExitFilterMode()
				return s, nil
			}
			var fcmd tea.Cmd
			s.queryFilter, fcmd = s.queryFilter.Update(msg)
			return s, fcmd
		}

		if s.selectingSeverity {
			switch msg.String() {
			case "up", "k":
				s.severitySelect.SelectPrev()
			case "down", "j":
				s.severitySelect.SelectNext()
			case "enter":
				s.severityThreshold = s.severitySelect.Value()
				s.selectingSeverity = false
				return s, s.Refresh()
			case "esc":
				s.selectingSeverity = false
			}
			return s, nil
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()

		case "/": // Edit the free-text LQL query
			s.queryFilter.TextInput.SetValue(s.userQuery)
			return s, s.queryFilter.EnterFilterMode()

		case "s": // Open the minimum-severity picker
			s.severitySelect = components.NewSelect("Minimum Severity", SeverityLevels, severityLevelLabels, s.severityThreshold)
			s.selectingSeverity = true
			return s, nil

		case "L": // Toggle live-tail mode
			s.SetLive(!s.live)
			return s, s.Refresh()

		case "R":
			s.resourcesMode = true
			s.resourceViewState = ResourceViewList
			return s, tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab))

		case "enter":
			if idx := s.table.Cursor(); idx >= 0 && idx < len(s.entries) {
				s.selectedEntry = &s.entries[idx]
				s.viewingDetail = true
				s.detailViewport.SetContent(s.renderDetailContent())
				s.detailViewport.GotoTop()
			}
			return s, nil

		case "p": // Previous / Older (Fetch next API page)
			if s.nextPageToken != "" {
				s.spinner.Start("")
				// Push current token to stack (which represents state of "newer" page)
				s.tokenStack = append(s.tokenStack, s.currentToken)
				// Update current token
				s.currentToken = s.nextPageToken
				return s, s.fetchEntriesCmd(s.currentToken)
			}
		case "n": // Next / Newer (Pop stack)
			if len(s.tokenStack) > 0 {
				s.spinner.Start("")
				// Pop last token
				lastIdx := len(s.tokenStack) - 1
				prevToken := s.tokenStack[lastIdx]
				s.tokenStack = s.tokenStack[:lastIdx]

				// Update current token
				s.currentToken = prevToken
				return s, s.fetchEntriesCmd(s.currentToken)
			}
		case "esc", "q":
			if s.returnTo != "" {
				dest := s.returnTo
				s.returnTo = "" // Reset
				return s, func() tea.Msg { return core.SwitchToServiceMsg{Service: dest} }
			}
			return s, nil
		}

		// Pass to table for navigation
		var updatedTable *components.StandardTable
		updatedTable, cmd = s.table.Update(msg)
		s.table = updatedTable
		return s, cmd
	}

	return s, cmd
}

// updateResourcesMode handles all key input while resourcesMode is active:
// tab switching, list navigation, and the create/delete/grant-IAM sub-flows
// for sinks/metrics/buckets/views.
func (s *Service) updateResourcesMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch s.resourceViewState {
	case ResourceViewCreate:
		result, formCmd := s.resourceCreateForm.Update(msg)
		if result.Cancelled {
			s.resourceViewState = ResourceViewList
			return s, nil
		}
		if result.Submitted {
			s.resourceViewState = ResourceViewList
			return s, s.createResourceCmd()
		}
		return s, formCmd

	case ResourceViewGrantIAM:
		result, formCmd := s.resourceCreateForm.Update(msg)
		if result.Cancelled {
			s.resourceViewState = ResourceViewList
			return s, nil
		}
		if result.Submitted {
			v := s.resourceCreateForm.Values()
			s.resourceViewState = ResourceViewList
			return s, s.grantViewIAMCmd(v["Role"], v["Member"])
		}
		return s, formCmd

	case ResourceViewConfirmation:
		switch msg.String() {
		case "y", "enter":
			s.resourceViewState = ResourceViewList
			return s, s.deleteResourceCmd()
		case "n", "esc", "q":
			s.resourceViewState = ResourceViewList
			s.pendingResourceDelete = ""
			return s, nil
		}
		return s, nil
	}

	switch msg.String() {
	case "R", "esc":
		s.resourcesMode = false
		return s, nil
	case "r":
		return s, tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab))
	case "[":
		s.resourceTab = prevResourceTab(s.resourceTab)
		return s, tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab))
	case "]":
		s.resourceTab = nextResourceTab(s.resourceTab)
		return s, tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab))
	case "n":
		s.resourceCreateForm = s.newResourceCreateForm()
		s.resourceViewState = ResourceViewCreate
		return s, nil
	case "g":
		if s.resourceTab == ResourceTabViews {
			if idx := s.viewTable.Cursor(); idx >= 0 && idx < len(s.views) {
				s.resourceCreateForm = components.NewForm("Grant IAM Binding: "+s.views[idx].Name, []components.FormField{
					{Label: "Role", Placeholder: "roles/logging.viewAccessor", Required: true},
					{Label: "Member", Placeholder: "user:name@example.com", Required: true},
				})
				s.resourceViewState = ResourceViewGrantIAM
			}
		}
		return s, nil
	case "d":
		name := s.selectedResourceName()
		if name != "" {
			s.pendingResourceDelete = name
			s.resourceViewState = ResourceViewConfirmation
		}
		return s, nil
	}

	updatedTable, cmd := s.activeResourceTable().Update(msg)
	s.setActiveResourceTable(updatedTable)
	return s, cmd
}

// selectedResourceName returns the identifying name of the row currently
// under the cursor in the active resource tab, or "" if none.
func (s *Service) selectedResourceName() string {
	switch s.resourceTab {
	case ResourceTabSinks:
		if idx := s.sinkTable.Cursor(); idx >= 0 && idx < len(s.sinks) {
			return s.sinks[idx].Name
		}
	case ResourceTabMetrics:
		if idx := s.metricTable.Cursor(); idx >= 0 && idx < len(s.metrics) {
			return s.metrics[idx].Name
		}
	case ResourceTabBuckets:
		if idx := s.bucketTable.Cursor(); idx >= 0 && idx < len(s.buckets) {
			return s.buckets[idx].Name
		}
	case ResourceTabViews:
		if idx := s.viewTable.Cursor(); idx >= 0 && idx < len(s.views) {
			return s.views[idx].FullName
		}
	}
	return ""
}

// newResourceCreateForm builds the FormModel for the active resource tab's
// Create flow.
func (s *Service) newResourceCreateForm() components.FormModel {
	switch s.resourceTab {
	case ResourceTabSinks:
		return components.NewForm("New Sink", []components.FormField{
			{Label: "Name", Required: true},
			{Label: "Destination", Placeholder: "storage.googleapis.com/my-bucket", Required: true},
			{Label: "Filter", Placeholder: `severity>=ERROR`},
		})
	case ResourceTabMetrics:
		return components.NewForm("New Log Metric", []components.FormField{
			{Label: "Name", Required: true},
			{Label: "Description"},
			{Label: "Filter", Placeholder: `severity>=ERROR`, Required: true},
		})
	case ResourceTabBuckets:
		return components.NewForm("New Log Bucket", []components.FormField{
			{Label: "Bucket ID", Required: true},
			{Label: "Location", Default: "global", Required: true},
			{Label: "Retention Days", Default: "30", Required: true, Validate: func(v string) string {
				if _, err := strconv.ParseInt(v, 10, 64); err != nil {
					return "must be an integer"
				}
				return ""
			}},
		})
	default: // ResourceTabViews
		return components.NewForm("New Log View: bucket "+s.viewBucketID, []components.FormField{
			{Label: "View ID", Required: true},
			{Label: "Filter", Placeholder: `resource.type="gce_instance"`},
		})
	}
}

// createResourceCmd fires the Create API call for the active resource tab
// using the current resourceCreateForm values.
func (s *Service) createResourceCmd() tea.Cmd {
	v := s.resourceCreateForm.Values()
	tab := s.resourceTab
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		var err error
		var name string
		switch tab {
		case ResourceTabSinks:
			name = v["Name"]
			err = s.client.CreateSink(name, v["Destination"], v["Filter"])
		case ResourceTabMetrics:
			name = v["Name"]
			err = s.client.CreateLogMetric(name, v["Description"], v["Filter"])
		case ResourceTabBuckets:
			name = v["Bucket ID"]
			days, _ := strconv.ParseInt(v["Retention Days"], 10, 64)
			err = s.client.CreateLogBucket(v["Location"], name, days)
		case ResourceTabViews:
			name = v["View ID"]
			err = s.client.CreateLogView(s.viewLocation, s.viewBucketID, name, v["Filter"])
		}
		if err != nil {
			return resourceActionResultMsg{err: err, action: "create", name: name}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Creating %s...", name), action: "create", name: name}
	}
}

// deleteResourceCmd fires the Delete API call for pendingResourceDelete in
// the active resource tab.
func (s *Service) deleteResourceCmd() tea.Cmd {
	tab := s.resourceTab
	name := s.pendingResourceDelete
	location := s.viewLocation
	s.pendingResourceDelete = ""
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		var err error
		switch tab {
		case ResourceTabSinks:
			err = s.client.DeleteSink(name)
		case ResourceTabMetrics:
			err = s.client.DeleteLogMetric(name)
		case ResourceTabBuckets:
			err = s.client.DeleteLogBucket(location, name)
		case ResourceTabViews:
			err = s.client.DeleteLogView(name)
		}
		if err != nil {
			return resourceActionResultMsg{err: err, action: "delete", name: name}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleting %s...", name), action: "delete", name: name}
	}
}

// grantViewIAMCmd grants role to member on the view currently under the
// cursor in the Views tab.
func (s *Service) grantViewIAMCmd(role, member string) tea.Cmd {
	idx := s.viewTable.Cursor()
	if idx < 0 || idx >= len(s.views) {
		return nil
	}
	fullName := s.views[idx].FullName
	return func() tea.Msg {
		if s.client == nil {
			return resourceActionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddLogViewIAMBinding(fullName, role, member); err != nil {
			return resourceActionResultMsg{err: err, action: "grant", name: fullName}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Granted %s to %s", role, member), action: "grant", name: fullName}
	}
}

// fetchResourceTabCmd fetches the list for the given resource tab.
func (s *Service) fetchResourceTabCmd(tab ResourceTab) tea.Cmd {
	switch tab {
	case ResourceTabSinks:
		return s.fetchSinksCmd()
	case ResourceTabMetrics:
		return s.fetchMetricsCmd()
	case ResourceTabBuckets:
		return s.fetchBucketsCmd()
	default:
		return s.fetchViewsCmd()
	}
}

func (s *Service) fetchSinksCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListSinks()
		if err != nil {
			return errMsg(err)
		}
		return sinksMsg(items)
	}
}

func (s *Service) fetchMetricsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListLogMetrics()
		if err != nil {
			return errMsg(err)
		}
		return metricsMsg(items)
	}
}

func (s *Service) fetchBucketsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListLogBuckets(s.viewLocation)
		if err != nil {
			return errMsg(err)
		}
		return bucketsMsg(items)
	}
}

func (s *Service) fetchViewsCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		items, err := s.client.ListLogViews(s.viewLocation, s.viewBucketID)
		if err != nil {
			return errMsg(err)
		}
		return viewsMsg(items)
	}
}

func (s *Service) updateSinkTable() {
	rows := make([]table.Row, len(s.sinks))
	for i, sk := range s.sinks {
		rows[i] = table.Row{sk.Name, sk.Destination, fmt.Sprintf("%t", sk.Disabled)}
	}
	s.sinkTable.SetRows(rows)
}

func (s *Service) updateMetricTable() {
	rows := make([]table.Row, len(s.metrics))
	for i, m := range s.metrics {
		rows[i] = table.Row{m.Name, m.Description, m.Filter}
	}
	s.metricTable.SetRows(rows)
}

func (s *Service) updateBucketTable() {
	rows := make([]table.Row, len(s.buckets))
	for i, b := range s.buckets {
		rows[i] = table.Row{b.Name, fmt.Sprintf("%d", b.RetentionDays), fmt.Sprintf("%t", b.Locked)}
	}
	s.bucketTable.SetRows(rows)
}

func (s *Service) updateViewTable() {
	rows := make([]table.Row, len(s.views))
	for i, v := range s.views {
		rows[i] = table.Row{v.Name, v.Filter}
	}
	s.viewTable.SetRows(rows)
}

// SetFilter sets the base resource-scoping filter (used by other components
// to jump to logs, e.g. SwitchToLogsMsg). It is combined with the user's own
// query and severity threshold by composeFilter, not replaced by them.
func (s *Service) SetFilter(filter string) {
	s.baseFilter = filter
}

// composeFilter builds the final LQL filter sent to the API by ANDing
// together the resource-scoping base filter (set externally via SetFilter),
// the user's free-text query (typed in this view via "/"), and the severity
// threshold (cycled with "s"). Empty parts are omitted. This does not
// validate LQL syntax -- each part is treated as an opaque string, same
// convention as the sinks/metrics Filter form fields elsewhere in this
// package.
func (s *Service) composeFilter() string {
	return composeFilter(s.baseFilter, s.userQuery, s.severityThreshold)
}

func composeFilter(base, query, severity string) string {
	var parts []string
	if base != "" {
		parts = append(parts, base)
	}
	if q := strings.TrimSpace(query); q != "" {
		parts = append(parts, "("+q+")")
	}
	if severity != "" && severity != "DEFAULT" {
		parts = append(parts, fmt.Sprintf("severity>=%s", severity))
	}
	return strings.Join(parts, " AND ")
}

// SetLive toggles live-tail mode (see the `live` field doc comment). The
// next Refresh -- either the one SwitchToLogsMsg triggers on entry, or a
// manual "r" -- starts (or resumes) tailing accordingly.
func (s *Service) SetLive(live bool) {
	s.live = live
}

// SetReturnTo sets the service to return to when Esc is pressed
func (s *Service) SetReturnTo(service string) {
	s.returnTo = service
}

// SetHeading sets the custom heading
func (s *Service) SetHeading(heading string) {
	s.heading = heading
}

func (s *Service) adjustTableColumns() {
	// Calculate available width for message column
	// Time=19, Sev=8, padding/borders ~6
	msgWidth := s.width - 19 - 8 - 10
	if msgWidth < 30 {
		msgWidth = 30
	}
	if msgWidth > 120 {
		msgWidth = 120
	}

	columns := []table.Column{
		{Title: "Time", Width: 19},
		{Title: "Sev", Width: 8},
		{Title: "Message", Width: msgWidth},
	}
	s.table.SetColumns(columns)
}

func (s *Service) updateTable() {
	rows := make([]table.Row, len(s.entries))
	for i, e := range s.entries {
		ts := e.Timestamp.Local().Format("01-02 15:04:05")
		sev := formatSeverityCell(e.Severity)

		// Truncate message for table display
		msg := e.Payload
		// Remove newlines for table display
		msg = strings.ReplaceAll(msg, "\n", " ")
		if len(msg) > 100 {
			msg = msg[:97] + "..."
		}

		rows[i] = table.Row{ts, sev, msg}
	}
	s.table.SetRows(rows)
}

// formatSeverityShort renders a severity as the full padded/background
// RenderStatus badge, for the (unconstrained-width) entry detail view only
// -- see formatSeverityCell for the table-safe plain-text version.
func formatSeverityShort(severity string) string {
	return components.RenderStatus(severityAbbrev(severity))
}

// formatSeverityCell renders a severity as plain text (no lipgloss styling)
// for the log-entries table's narrow, fixed-width "Sev" column. The table
// library truncates overflowing cells with a rune-width algorithm that
// isn't ANSI-escape-aware, so a styled badge here (as this used to be)
// silently corrupts the row -- mangling the severity text itself and
// bleeding stray characters into the table's own border, exactly like the
// Cloud Build Builds list's Status column before it got the same fix.
func formatSeverityCell(severity string) string {
	return severityAbbrev(severity)
}

// severityAbbrev shortens the longer Cloud Logging severity names so they
// comfortably fit the table's 8-wide Sev column.
func severityAbbrev(severity string) string {
	switch severity {
	case "EMERGENCY":
		return "EMERG"
	case "CRITICAL":
		return "CRIT"
	case "ERROR":
		return "ERROR"
	case "WARNING":
		return "WARN"
	case "NOTICE":
		return "NOTICE"
	case "INFO":
		return "INFO"
	case "DEBUG":
		return "DEBUG"
	default:
		return "DEFAULT"
	}
}

// severityLevelLabels are the display labels shown in the severity picker,
// parallel to SeverityLevels -- only DEFAULT gets a friendlier label since
// "no filter" is clearer to a user than the raw enum value.
var severityLevelLabels = []string{
	"Any (no filter)", "DEBUG", "INFO", "NOTICE", "WARNING", "ERROR", "CRITICAL", "ALERT", "EMERGENCY",
}

func (s *Service) fetchEntriesCmd(token string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		// Use the composed filter (base + user query + severity threshold)
		filter := s.composeFilter()
		pageSize := defaultPageSize

		entries, nextToken, err := s.client.ListEntries(context.Background(), filter, pageSize, token)
		if err != nil {
			return errMsg(err)
		}
		return entriesMsg{entries: entries, nextToken: nextToken}
	}
}

// fetchLiveEntriesCmd polls for entries newer than lastEntryTime (or, on the
// first call after SetLive(true)/Refresh, the filter's full matching
// history), oldest-first, for live-tail mode.
func (s *Service) fetchLiveEntriesCmd() tea.Cmd {
	filter := s.composeFilter()
	since := s.lastEntryTime
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		entries, err := s.client.ListEntriesSince(context.Background(), filter, since)
		if err != nil {
			return errMsg(err)
		}
		return liveEntriesMsg{filter: filter, entries: entries}
	}
}

func (s *Service) Refresh() tea.Cmd {
	s.spinner.Start("")
	s.currentToken = ""
	s.tokenStack = nil
	if s.live {
		s.entries = nil
		s.lastEntryTime = time.Time{}
		s.lastEntryInsertIDs = nil
		return s.fetchLiveEntriesCmd()
	}
	return s.fetchEntriesCmd("")
}

func (s *Service) Reset() {
	s.err = nil
	s.viewingDetail = false
	s.selectedEntry = nil
	s.table.SetCursor(0)
	s.SetHeading("")
	s.currentToken = ""
	s.tokenStack = nil
	s.baseFilter = ""
	s.userQuery = ""
	s.severityThreshold = ""
	s.queryFilter.ExitFilterMode()
	s.queryFilter.TextInput.SetValue("")
	s.selectingSeverity = false
	s.live = false
	s.lastEntryTime = time.Time{}
	s.lastEntryInsertIDs = nil
	s.resourcesMode = false
	s.resourceViewState = ResourceViewList
	s.pendingResourceDelete = ""
}

// IsRootView reports whether the top-level UI's Tab/Shift+Tab -> "]"/"["
// remap (see internal/ui/model.go) is safe to apply. It must be false while
// the query filter is being edited or the severity picker is open, since
// Tab there means "accept autocomplete suggestion"/list-navigation, not
// "cycle resource tab" -- otherwise the remap turns a real Tab keystroke
// into a synthetic "]" KeyRunes event that the query text input just
// inserts as a literal character.
func (s *Service) IsRootView() bool {
	return !s.viewingDetail && !s.resourcesMode && !s.queryFilter.Active && !s.selectingSeverity
}

// NextTab/PrevTab implement services.TabCycler: cycling the Sinks/Metrics/
// Buckets/Views resource tabs only makes sense while resourcesMode is on
// and its own list (not a create/grant/confirm sub-flow) is showing.
func (s *Service) NextTab() (tea.Cmd, bool) {
	if !s.resourcesMode || s.resourceViewState != ResourceViewList {
		return nil, false
	}
	s.resourceTab = nextResourceTab(s.resourceTab)
	return tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab)), true
}

func (s *Service) PrevTab() (tea.Cmd, bool) {
	if !s.resourcesMode || s.resourceViewState != ResourceViewList {
		return nil, false
	}
	s.resourceTab = prevResourceTab(s.resourceTab)
	return tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab)), true
}
