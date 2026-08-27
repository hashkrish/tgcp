package filestore

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

type instancesMsg []Instance
type errMsg error

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Instance]

	instances []Instance
	spinner   components.SpinnerModel
	err       error

	viewState        ViewState
	selectedInstance *Instance

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 22},
		{Title: "Zone/Region", Width: 15},
		{Title: "Tier", Width: 14},
		{Title: "Capacity", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Network", Width: 16},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter instances..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredInstances, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Filestore"
}

func (s *Service) ShortName() string {
	return "filestore"
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

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchInstancesCmd(false),
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
		s.fetchInstancesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedInstance = nil
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
		return s, tea.Batch(s.fetchInstancesCmd(false), s.tick())

	case instancesMsg:
		s.spinner.Stop()
		s.instances = msg
		s.filterSession.Apply(s.instances)
		if s.selectedInstance != nil {
			for i := range s.instances {
				if s.instances[i].Name == s.selectedInstance.Name {
					s.selectedInstance = &s.instances[i]
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
				instances := s.getFilteredInstances(s.instances, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(instances) {
					s.selectedInstance = &instances[idx]
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
				s.selectedInstance = nil
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
		return components.RenderError(s.err, s.Name(), "Instances")
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
		"Instances",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")

	if len(s.instances) == 0 {
		content.WriteString(components.EmptyState("filestore instances"))
		return content.String()
	}

	content.WriteString(s.table.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchInstancesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("filestore_instances:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Instance); ok {
					return instancesMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListInstances(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return instancesMsg(items)
	}
}

func (s *Service) updateTable(items []Instance) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.Tier,
			fmt.Sprintf("%d GB", item.CapacityGB),
			item.State,
			item.Network,
		}
	}
	s.table.SetRows(rows)
}

// getFilteredInstances returns filtered instances based on the query string
func (s *Service) getFilteredInstances(instances []Instance, query string) []Instance {
	if query == "" {
		return instances
	}
	return components.FilterSlice(instances, query, func(inst Instance, q string) bool {
		return components.ContainsMatch(inst.Name, inst.Location, inst.Tier, inst.State, inst.Network)(q)
	})
}
