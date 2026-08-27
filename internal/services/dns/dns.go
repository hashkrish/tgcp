package dns

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
	ViewZones ViewState = iota
	ViewRecords
)

type zonesMsg []Zone
type recordsMsg []RecordSet
type errMsg error

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	zoneTable   *components.StandardTable
	recordTable *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Zone]

	zones   []Zone
	records []RecordSet
	spinner components.SpinnerModel
	err     error

	viewState    ViewState
	selectedZone *Zone

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	zoneColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "DNS Name", Width: 30},
		{Title: "Visibility", Width: 12},
		{Title: "Description", Width: 30},
	}
	zt := components.NewStandardTable(zoneColumns)

	recordColumns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Type", Width: 8},
		{Title: "TTL", Width: 8},
		{Title: "Data", Width: 40},
	}
	rt := components.NewStandardTable(recordColumns)

	svc := &Service{
		zoneTable:   zt,
		recordTable: rt,
		filter:      components.NewFilterWithPlaceholder("Filter zones..."),
		spinner:     components.NewSpinner(),
		viewState:   ViewZones,
		cache:       cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredZones, svc.updateZoneTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud DNS"
}

func (s *Service) ShortName() string {
	return "dns"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewZones:
		return "r:Refresh  /:Filter  Ent:Records"
	case ViewRecords:
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
	return tea.Batch(s.spinner.Start(""), s.fetchZonesCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	if s.viewState == ViewRecords && s.selectedZone != nil {
		return tea.Batch(
			s.spinner.Start(""),
			s.fetchRecordsCmd(s.selectedZone.Name),
		)
	}
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchZonesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewZones
	s.selectedZone = nil
	s.records = nil
	s.err = nil
	s.zoneTable.SetCursor(0)
	s.recordTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewZones
}

func (s *Service) Focus() {
	s.zoneTable.Focus()
}

func (s *Service) Blur() {
	s.zoneTable.Blur()
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
		if s.viewState == ViewZones {
			return s, tea.Batch(s.fetchZonesCmd(false), s.tick())
		}
		return s, s.tick()

	case zonesMsg:
		s.spinner.Stop()
		s.zones = msg
		s.filterSession.Apply(s.zones)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case recordsMsg:
		s.spinner.Stop()
		s.records = msg
		s.updateRecordTable(msg)
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case tea.WindowSizeMsg:
		s.zoneTable.HandleWindowSizeDefault(msg)
		s.recordTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		switch s.viewState {
		case ViewZones:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.zoneTable.Update(msg)
			s.zoneTable = updatedTable
			return s, cmd
		case ViewRecords:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.recordTable.Update(msg)
			s.recordTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if s.viewState == ViewZones {
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
			zones := s.getFilteredZones(s.zones, s.filter.Value())
			if idx := s.zoneTable.Cursor(); idx >= 0 && idx < len(zones) {
				s.selectedZone = &zones[idx]
				s.viewState = ViewRecords
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchRecordsCmd(s.selectedZone.Name),
				)
			}
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.zoneTable.Update(msg)
		s.zoneTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewRecords {
		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "esc", "q":
			s.viewState = ViewZones
			s.selectedZone = nil
			s.records = nil
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.recordTable.Update(msg)
		s.recordTable = updatedTable
		return s, cmd
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// Views
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Zones")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewRecords {
		return s.renderRecordsView()
	}

	return s.renderListView()
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Zones",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.zoneTable.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchZonesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dns_zones:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Zone); ok {
					return zonesMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListZones(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return zonesMsg(items)
	}
}

func (s *Service) fetchRecordsCmd(zoneName string) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dns_records:%s:%s", s.projectID, zoneName)

		if s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]RecordSet); ok {
					return recordsMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListRecordSets(s.projectID, zoneName)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return recordsMsg(items)
	}
}

func (s *Service) updateZoneTable(items []Zone) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		desc := item.Description
		if desc == "" {
			desc = "-"
		}
		rows[i] = table.Row{
			item.Name,
			item.DNSName,
			item.Visibility,
			desc,
		}
	}
	s.zoneTable.SetRows(rows)
}

func (s *Service) updateRecordTable(items []RecordSet) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Type,
			fmt.Sprintf("%d", item.TTL),
			strings.Join(item.Rrdatas, ", "),
		}
	}
	s.recordTable.SetRows(rows)
}

// getFilteredZones returns filtered zones based on the query string
func (s *Service) getFilteredZones(zones []Zone, query string) []Zone {
	if query == "" {
		return zones
	}
	return components.FilterSlice(zones, query, func(zone Zone, q string) bool {
		return components.ContainsMatch(zone.Name, zone.DNSName, zone.Visibility, zone.Description)(q)
	})
}
