package secrets

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

const CacheTTL = 60 * time.Second // Secrets rarely change

// -----------------------------------------------------------------------------
// Message Types
// -----------------------------------------------------------------------------

type tickMsg time.Time
type secretsMsg []Secret
type versionsMsg []SecretVersion
type errMsg error
type revealedMsg string

// revealErrMsg is a distinct concrete type (not `type revealErrMsg error`)
// so it doesn't collide with errMsg in the tea.Msg type switch below —
// two named interface types with the same method set (both just wrapping
// `error`) match identically in a type switch, silently making whichever
// case is listed second unreachable (caught by staticcheck's SA4020).
type revealErrMsg struct{ err error }

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

// Service implements the services.Service interface for Secret Manager
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
	filterSession components.FilterSession[Secret]
	spinner       components.SpinnerModel

	// Data State
	secrets  []Secret
	versions []SecretVersion
	err      error

	// View State
	viewState       ViewState
	selectedSecret  *Secret
	selectedVersion *SecretVersion

	// Reveal state — session-only, never persisted or cached. Only the
	// "is currently revealed" boolean and the fetched value live here, and
	// both are cleared whenever the user navigates away from the version
	// detail view (see clearReveal).
	revealed      bool
	revealedValue string
	revealErr     error

	// Cache
	cache *core.Cache
}

// NewService creates a new Secret Manager service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Replication", Width: 15},
		{Title: "Labels", Width: 25},
		{Title: "Created", Width: 20},
	}

	t := components.NewStandardTable(columns)

	versionColumns := []table.Column{
		{Title: "Version", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Created", Width: 25},
	}
	vt := components.NewStandardTable(versionColumns)

	svc := &Service{
		table:        t,
		versionTable: vt,
		filter:       components.NewFilterWithPlaceholder("Filter secrets..."),
		spinner:      components.NewSpinner(),
		viewState:    ViewList,
		cache:        cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredSecrets, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Secret Manager"
}

func (s *Service) ShortName() string {
	return "secrets"
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
		if s.revealed {
			return "v:Hide Value  Esc/q:Back"
		}
		return "v:Reveal Value  Esc/q:Back"
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
	return tea.Batch(s.spinner.Start(""), s.fetchSecretsCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchSecretsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedSecret = nil
	s.versions = nil
	s.err = nil
	s.table.SetCursor(0)
	s.versionTable.SetCursor(0)
	s.filter.ExitFilterMode()
	s.clearReveal()
}

// clearReveal wipes any revealed secret value and the revealed flag. It is
// called any time the user leaves the version detail view, so a revealed
// value never survives navigation and is never cached anywhere.
func (s *Service) clearReveal() {
	s.selectedVersion = nil
	s.revealed = false
	s.revealedValue = ""
	s.revealErr = nil
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
		return s, tea.Batch(s.fetchSecretsCmd(false), s.tick())

	case secretsMsg:
		s.spinner.Stop()
		s.secrets = msg
		s.filterSession.Apply(s.secrets)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case versionsMsg:
		s.spinner.Stop()
		s.versions = msg
		s.updateVersionTable(msg)
		return s, nil

	case revealedMsg:
		s.spinner.Stop()
		s.revealed = true
		s.revealedValue = string(msg)
		s.revealErr = nil
		return s, nil

	case revealErrMsg:
		s.spinner.Stop()
		s.revealed = false
		s.revealedValue = ""
		s.revealErr = msg.err
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
		case "enter":
			secrets := s.getCurrentSecrets()
			if idx := s.table.Cursor(); idx >= 0 && idx < len(secrets) {
				s.selectedSecret = &secrets[idx]
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
			s.selectedSecret = nil
			s.versions = nil
			return s, nil
		case "v":
			// Fetch versions for selected secret
			if s.selectedSecret != nil {
				s.viewState = ViewVersions
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchVersionsCmd(s.selectedSecret.FullName),
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
				s.clearReveal()
				s.selectedVersion = &s.versions[idx]
				s.viewState = ViewVersionDetail
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
			s.clearReveal()
			return s, nil
		case "v":
			// Explicit, deliberate reveal/hide toggle. The value is never
			// fetched automatically just from navigating here — only this
			// keypress triggers the API call.
			if s.selectedVersion == nil {
				return s, nil
			}
			if s.revealed {
				// Hide: drop the cached plaintext immediately.
				s.revealed = false
				s.revealedValue = ""
				return s, nil
			}
			return s, tea.Batch(
				s.spinner.Start(""),
				s.fetchRevealCmd(s.selectedVersion.FullName),
			)
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Secrets")
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

	if len(s.secrets) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			breadcrumb,
			s.filter.View(),
			components.EmptyState("secrets"),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.filter.View(),
		s.table.View(),
	)
}

func (s *Service) renderDetailView() string {
	if s.selectedSecret == nil {
		return "No secret selected"
	}

	sec := s.selectedSecret

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		sec.Name,
	)

	// Format labels
	labelStr := "-"
	if len(sec.Labels) > 0 {
		var labels []string
		for k, v := range sec.Labels {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		}
		labelStr = fmt.Sprintf("%v", labels)
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Secret Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: sec.Name},
			{Key: "Replication", Value: sec.Replication},
			{Key: "Labels", Value: labelStr},
			{Key: "Created", Value: sec.CreateTime.Local().Format("2006-01-02 15:04:05")},
			{Key: "Resource Name", Value: sec.FullName},
		},
		FooterHint: "v Versions | q Back",
	})

	// Security note
	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true).
		Render("Note: Secret values are not displayed for security reasons.")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		note,
	)
}

func (s *Service) renderVersionsView() string {
	if s.selectedSecret == nil {
		return "No secret selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedSecret.Name,
		"Versions",
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.versionTable.View(),
	)
}

func (s *Service) renderVersionDetailView() string {
	if s.selectedSecret == nil || s.selectedVersion == nil {
		return "No version selected"
	}

	ver := s.selectedVersion

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedSecret.Name,
		"Versions",
		ver.Name,
	)

	rows := []components.KeyValue{
		{Key: "Version", Value: ver.Name},
		{Key: "State", Value: ver.State},
		{Key: "Created", Value: ver.CreateTime.Local().Format("2006-01-02 15:04:05")},
		{Key: "Resource Name", Value: ver.FullName},
	}

	var valueBlock string
	switch {
	case s.revealErr != nil:
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorError).
			Render(fmt.Sprintf("Failed to reveal value: %v", s.revealErr))
	case s.revealed:
		warning := lipgloss.NewStyle().
			Foreground(styles.ColorWarning).
			Bold(true).
			Render("[!] VISIBLE - press v to hide")
		value := lipgloss.NewStyle().
			Foreground(styles.ColorTextPrimary).
			Render(s.revealedValue)
		valueBlock = lipgloss.JoinVertical(lipgloss.Left, warning, "", value)
	default:
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Italic(true).
			Render("Value hidden. Press v to reveal (fetches from Secret Manager).")
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title:      "Secret Version Details",
		Rows:       rows,
		FooterHint: "v Reveal/Hide | q Back",
	})

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

func (s *Service) fetchSecretsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("secrets:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if secrets, ok := val.([]Secret); ok {
					return secretsMsg(secrets)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		secrets, err := s.client.ListSecrets(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, secrets, CacheTTL)
		}

		return secretsMsg(secrets)
	}
}

func (s *Service) fetchVersionsCmd(secretName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		versions, err := s.client.ListVersions(secretName)
		if err != nil {
			return errMsg(err)
		}

		return versionsMsg(versions)
	}
}

// fetchRevealCmd fetches the plaintext value for a specific secret version.
// This is only ever invoked from the explicit "v" (reveal) keypress in
// ViewVersionDetail — never from Init/Refresh/tick or as a side effect of
// simply navigating to a version. The fetched value is never logged (see
// utils.Log usage elsewhere in this package — there is none here) and is
// only ever held in-memory in s.revealedValue, cleared on Hide or on
// leaving the view.
func (s *Service) fetchRevealCmd(versionFullName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revealErrMsg{err: fmt.Errorf("client not initialized")}
		}

		value, err := s.client.AccessVersion(versionFullName)
		if err != nil {
			return revealErrMsg{err: err}
		}

		return revealedMsg(value)
	}
}

// -----------------------------------------------------------------------------
// Table Updates
// -----------------------------------------------------------------------------

func (s *Service) updateTable(secrets []Secret) {
	rows := make([]table.Row, len(secrets))
	for i, sec := range secrets {
		// Format labels for display
		labelStr := "-"
		if len(sec.Labels) > 0 {
			count := len(sec.Labels)
			if count == 1 {
				for k, v := range sec.Labels {
					labelStr = fmt.Sprintf("%s=%s", k, v)
				}
			} else {
				labelStr = fmt.Sprintf("%d labels", count)
			}
		}

		rows[i] = table.Row{
			sec.Name,
			sec.Replication,
			labelStr,
			sec.CreateTime.Local().Format("2006-01-02 15:04"),
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateVersionTable(versions []SecretVersion) {
	rows := make([]table.Row, len(versions))
	for i, v := range versions {
		rows[i] = table.Row{
			v.Name,
			components.RenderStatus(v.State),
			v.CreateTime.Local().Format("2006-01-02 15:04:05"),
		}
	}
	s.versionTable.SetRows(rows)
}

func (s *Service) getCurrentSecrets() []Secret {
	return s.getFilteredSecrets(s.secrets, s.filter.Value())
}

func (s *Service) getFilteredSecrets(secrets []Secret, query string) []Secret {
	if query == "" {
		return secrets
	}
	return components.FilterSlice(secrets, query, func(sec Secret, q string) bool {
		// Build label string for search
		var labelStr string
		for k, v := range sec.Labels {
			labelStr += k + "=" + v + " "
		}
		return components.ContainsMatch(sec.Name, sec.Replication, labelStr)(q)
	})
}
