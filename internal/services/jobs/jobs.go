// Package jobs implements the "Job History" view: a read-only, searchable
// list of every non-read (mutating) operation performed against GCP through
// tgcp, persisted locally (see internal/core/jobs.go) and independent of the
// currently active project or GCP credentials -- it has no api.go/*Client,
// unlike every other service package here.
package jobs

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

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
)

// jobsMsg carries a (re)load of the persisted job history.
type jobsMsg []core.Job

type Service struct {
	table *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[core.Job]

	jobs     []core.Job
	viewState ViewState
	selected *core.Job
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Time", Width: 19},
		{Title: "Project", Width: 20},
		{Title: "Service", Width: 12},
		{Title: "Resource", Width: 14},
		{Title: "Action", Width: 12},
		{Title: "Status", Width: 10},
	}

	svc := &Service{
		table:     components.NewStandardTable(columns),
		filter:    components.NewFilterWithPlaceholder("Filter jobs..."),
		viewState: ViewList,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredJobs, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Job History"
}

func (s *Service) ShortName() string {
	return "jobs"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewDetail {
		return "Esc/q:Back"
	}
	return "r:Refresh  /:Filter  Ent:Detail"
}

// -----------------------------------------------------------------------------
// Lifecycle
// -----------------------------------------------------------------------------

// InitService loads the persisted job history from disk. Unlike every other
// service, it ignores projectID -- job history is global, local-only state,
// not scoped to a GCP project or dependent on any GCP credentials, so this
// works identically under --demo mode with zero special-casing.
func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.loadJobs()
	return nil
}

// Reinit is called on every project switch. Job history doesn't depend on
// the active project, so this is a no-op beyond the base Reset.
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return nil
}

// Init satisfies tea.Model (Update's return type); the initial load happens
// in InitService instead, matching how the route dispatcher in
// internal/ui/model.go drives every service (via Refresh(), not Init()).
func (s *Service) Init() tea.Cmd {
	return nil
}

func (s *Service) Refresh() tea.Cmd {
	return func() tea.Msg {
		return jobsMsg(core.LoadJobs())
	}
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selected = nil
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

func (s *Service) loadJobs() {
	s.jobs = core.LoadJobs()
	s.filterSession.Apply(s.jobs)
}

// -----------------------------------------------------------------------------
// Update
// -----------------------------------------------------------------------------

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case jobsMsg:
		s.jobs = msg
		s.filterSession.Apply(s.jobs)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

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

			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "enter":
				jobs := s.getFilteredJobs(s.jobs, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(jobs) {
					s.selected = &jobs[idx]
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
				s.selected = nil
				return s, nil
			}
		}
	}
	return s, nil
}

// -----------------------------------------------------------------------------
// Data & Helpers
// -----------------------------------------------------------------------------

func (s *Service) updateTable(jobs []core.Job) {
	rows := make([]table.Row, len(jobs))
	for i, j := range jobs {
		rows[i] = table.Row{
			j.OccurredAt.Format("2006-01-02 15:04:05"),
			j.ProjectID,
			j.Service,
			j.Resource,
			j.Action,
			statusLabel(j.Status),
		}
	}
	s.table.SetRows(rows)
}

// getFilteredJobs returns filtered jobs based on the query string, matching
// against every column plus the error text (so e.g. searching "quota" finds
// failed jobs whose error message mentions it).
func (s *Service) getFilteredJobs(jobs []core.Job, query string) []core.Job {
	if query == "" {
		return jobs
	}
	return components.FilterSlice(jobs, query, func(j core.Job, q string) bool {
		return components.ContainsMatch(j.ProjectID, j.Service, j.Resource, j.Name, j.Action, j.Status, j.Error)(q)
	})
}

// statusLabel maps core.Job's lowercase Status ("success"/"failed") to the
// vocabulary components.RenderStatus/CategorizeStatus already recognizes
// (SUCCEEDED/FAILED), so the table/detail view get the same colored status
// badges every other service's Status column uses.
func statusLabel(status string) string {
	switch strings.ToLower(status) {
	case core.JobSuccess:
		return "SUCCEEDED"
	case core.JobFailed:
		return "FAILED"
	default:
		return fmt.Sprintf("%v", status)
	}
}
