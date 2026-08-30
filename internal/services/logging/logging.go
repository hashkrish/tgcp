package logging

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 10 * time.Second // Logs change frequently

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

	// Filter for API calls
	filter string

	// Detail view
	selectedEntry *LogEntry
	viewingDetail bool

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
	err error
	msg string
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
		table:        t,
		spinner:      components.NewSpinner(),
		cache:        cache,
		sinkTable:    sinkTable,
		metricTable:  metricTable,
		bucketTable:  bucketTable,
		viewTable:    viewTable,
		viewBucketID: "_Default",
		viewLocation: "global",
	}
	return s
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
		return "Esc/q:Back"
	}
	base := "r:Refresh  Enter:Detail  R:Resources"
	if s.returnTo != "" {
		base = fmt.Sprintf("Esc:Back to %s  r:Refresh  Enter:Detail  R:Resources", s.returnTo)
	} else {
		base += "  Esc/q:Back"
	}
	if len(s.tokenStack) > 0 {
		base += "  n:Newer"
	}
	if s.nextPageToken != "" {
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
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError} }
		}
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
			return s, nil
		}

		switch msg.String() {
		case "r":
			return s, s.Refresh()

		case "R":
			s.resourcesMode = true
			s.resourceViewState = ResourceViewList
			return s, tea.Batch(s.spinner.Start(""), s.fetchResourceTabCmd(s.resourceTab))

		case "enter":
			if idx := s.table.Cursor(); idx >= 0 && idx < len(s.entries) {
				s.selectedEntry = &s.entries[idx]
				s.viewingDetail = true
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
	case "[", "]", "tab":
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
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Creating %s...", name)}
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
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Deleting %s...", name)}
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
			return resourceActionResultMsg{err: err}
		}
		return resourceActionResultMsg{msg: fmt.Sprintf("Granted %s to %s", role, member)}
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

// SetFilter sets the filter string (used by other components to jump to logs)
func (s *Service) SetFilter(filter string) {
	s.filter = filter
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
		sev := formatSeverityShort(e.Severity)

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

func formatSeverityShort(severity string) string {
	switch severity {
	case "EMERGENCY":
		return components.RenderStatus("EMERG")
	case "CRITICAL":
		return components.RenderStatus("CRIT")
	case "ERROR":
		return components.RenderStatus("ERROR")
	case "WARNING":
		return components.RenderStatus("WARN")
	case "NOTICE":
		return components.RenderStatus("NOTICE")
	case "INFO":
		return components.RenderStatus("INFO")
	case "DEBUG":
		return components.RenderStatus("DEBUG")
	default:
		return components.RenderStatus("DEFAULT")
	}
}

func (s *Service) fetchEntriesCmd(token string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		// Use the stored filter value
		filter := s.filter
		pageSize := 25 // More entries fit now with compact table format

		entries, nextToken, err := s.client.ListEntries(context.Background(), filter, pageSize, token)
		if err != nil {
			return errMsg(err)
		}
		return entriesMsg{entries: entries, nextToken: nextToken}
	}
}

func (s *Service) Refresh() tea.Cmd {
	s.spinner.Start("")
	s.currentToken = ""
	s.tokenStack = nil
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
	s.filter = "" // Clear filter on reset
	s.resourcesMode = false
	s.resourceViewState = ResourceViewList
	s.pendingResourceDelete = ""
}

func (s *Service) IsRootView() bool {
	return !s.viewingDetail && !s.resourcesMode
}
