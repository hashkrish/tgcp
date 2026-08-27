package kms

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
	ViewRings ViewState = iota
	ViewKeys
)

type ringsMsg []KeyRing
type keysMsg []CryptoKey
type errMsg error

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	ringTable *components.StandardTable
	keyTable  *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[KeyRing]

	rings   []KeyRing
	keys    []CryptoKey
	spinner components.SpinnerModel
	err     error

	viewState    ViewState
	selectedRing *KeyRing

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	ringColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Location", Width: 15},
		{Title: "Created", Width: 20},
	}
	rt := components.NewStandardTable(ringColumns)

	keyColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Purpose", Width: 20},
		{Title: "Algorithm", Width: 28},
		{Title: "Protection", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Rotation", Width: 12},
	}
	kt := components.NewStandardTable(keyColumns)

	svc := &Service{
		ringTable: rt,
		keyTable:  kt,
		filter:    components.NewFilterWithPlaceholder("Filter key rings..."),
		spinner:   components.NewSpinner(),
		viewState: ViewRings,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredRings, svc.updateRingTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud KMS"
}

func (s *Service) ShortName() string {
	return "kms"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewRings:
		return "r:Refresh  /:Filter  Ent:Keys"
	case ViewKeys:
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

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchRingsCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	if s.viewState == ViewKeys && s.selectedRing != nil {
		return tea.Batch(
			s.spinner.Start(""),
			s.fetchKeysCmd(s.selectedRing.FullName),
		)
	}
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchRingsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewRings
	s.selectedRing = nil
	s.keys = nil
	s.err = nil
	s.ringTable.SetCursor(0)
	s.keyTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewRings
}

func (s *Service) Focus() {
	s.ringTable.Focus()
}

func (s *Service) Blur() {
	s.ringTable.Blur()
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
		if s.viewState == ViewRings {
			return s, tea.Batch(s.fetchRingsCmd(false), s.tick())
		}
		return s, s.tick()

	case ringsMsg:
		s.spinner.Stop()
		s.rings = msg
		s.filterSession.Apply(s.rings)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case keysMsg:
		s.spinner.Stop()
		s.keys = msg
		s.updateKeyTable(msg)
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case tea.WindowSizeMsg:
		s.ringTable.HandleWindowSizeDefault(msg)
		s.keyTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		switch s.viewState {
		case ViewRings:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.ringTable.Update(msg)
			s.ringTable = updatedTable
			return s, cmd
		case ViewKeys:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.keyTable.Update(msg)
			s.keyTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if s.viewState == ViewRings {
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
			rings := s.getFilteredRings(s.rings, s.filter.Value())
			if idx := s.ringTable.Cursor(); idx >= 0 && idx < len(rings) {
				s.selectedRing = &rings[idx]
				s.viewState = ViewKeys
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchKeysCmd(s.selectedRing.FullName),
				)
			}
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.ringTable.Update(msg)
		s.ringTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewKeys {
		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "esc", "q":
			s.viewState = ViewRings
			s.selectedRing = nil
			s.keys = nil
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.keyTable.Update(msg)
		s.keyTable = updatedTable
		return s, cmd
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// Views
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Key Rings")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewKeys {
		return s.renderKeysView()
	}

	return s.renderListView()
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.ringTable.View())
	return content.String()
}

func (s *Service) renderKeysView() string {
	ringName := ""
	if s.selectedRing != nil {
		ringName = s.selectedRing.Name
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
		ringName,
	))
	content.WriteString("\n")
	content.WriteString(s.keyTable.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchRingsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("kms_rings:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]KeyRing); ok {
					return ringsMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListKeyRings(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return ringsMsg(items)
	}
}

func (s *Service) fetchKeysCmd(ringName string) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("kms_keys:%s:%s", s.projectID, ringName)

		if s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]CryptoKey); ok {
					return keysMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		fullRingName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", s.projectID, s.selectedRing.Location, ringName)
		items, err := s.client.ListCryptoKeys(fullRingName)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return keysMsg(items)
	}
}

func (s *Service) updateRingTable(items []KeyRing) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.CreateTime,
		}
	}
	s.ringTable.SetRows(rows)
}

func (s *Service) updateKeyTable(items []CryptoKey) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rotation := item.RotationPeriod
		if rotation == "" {
			rotation = "-"
		}
		rows[i] = table.Row{
			item.Name,
			item.Purpose,
			item.Algorithm,
			item.ProtectionLevel,
			item.State,
			rotation,
		}
	}
	s.keyTable.SetRows(rows)
}

// getFilteredRings returns filtered key rings based on the query string
func (s *Service) getFilteredRings(rings []KeyRing, query string) []KeyRing {
	if query == "" {
		return rings
	}
	return components.FilterSlice(rings, query, func(ring KeyRing, q string) bool {
		return components.ContainsMatch(ring.Name, ring.Location)(q)
	})
}
