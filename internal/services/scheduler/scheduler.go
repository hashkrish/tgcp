package scheduler

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
	ViewCreate
	ViewUpdate
	ViewConfirmation
)

// newJobUpdateForm builds the FormModel for updating a job's cron schedule,
// seeded with its current value. Target/type changes require a full
// job replace and are out of scope.
func newJobUpdateForm(job Job) components.FormModel {
	return components.NewForm("Update Job Schedule: "+job.Name, []components.FormField{
		{Label: "Schedule (cron)", Default: job.Schedule, Placeholder: "*/5 * * * *", Required: true},
	})
}

type jobsMsg []Job
type errMsg error

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Job]

	jobs    []Job
	spinner components.SpinnerModel
	err     error

	viewState   ViewState
	selectedJob *Job

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "pause", "resume", "run"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Region", Width: 15},
		{Title: "Schedule", Width: 18},
		{Title: "Target", Width: 12},
		{Title: "State", Width: 12},
		{Title: "Next Run", Width: 20},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter jobs..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredJobs, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud Scheduler"
}

func (s *Service) ShortName() string {
	return "scheduler"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  n:New Job (HTTP)  u:Update  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  p:Pause  R:Resume  x:Run Now  d:Delete"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
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
	return s.tick()
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchJobsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedJob = nil
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
		return s, tea.Batch(s.fetchJobsCmd(false), s.tick())

	case jobsMsg:
		s.spinner.Stop()
		s.jobs = msg
		s.filterSession.Apply(s.jobs)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "delete" || s.pendingAction == "pause" || s.pendingAction == "resume" || s.pendingAction == "run" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				s.selectedJob = nil
				s.viewState = ViewList
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if msg.err != nil {
			if s.viewState == ViewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
		s.viewState = ViewList
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
		s.table.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				return s, s.submitCreateCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted && s.selectedJob != nil {
				return s, s.submitUpdateCmd(*s.selectedJob)
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
		}

		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("New Job (HTTP target)", []components.FormField{
					{Label: "Name", Placeholder: "my-job", Required: true},
					{Label: "Region", Placeholder: "us-central1", Required: true},
					{Label: "Schedule (cron)", Default: "*/5 * * * *", Required: true},
					{Label: "Target URI", Placeholder: "https://example.com/handler", Required: true},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				jobs := s.getFilteredJobs(s.jobs, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(jobs) {
					s.selectedJob = &jobs[idx]
					s.viewState = ViewDetail
				}
			case "u":
				jobs := s.getFilteredJobs(s.jobs, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(jobs) {
					s.selectedJob = &jobs[idx]
					s.updateForm = newJobUpdateForm(*s.selectedJob)
					s.viewState = ViewUpdate
					return s, nil
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
				s.selectedJob = nil
				return s, nil
			case "u":
				if s.selectedJob != nil {
					s.updateForm = newJobUpdateForm(*s.selectedJob)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "p": // Pause (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "pause"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "R": // Resume (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "resume"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x": // Run Now (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "run"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.selectedJob != nil {
					switch s.pendingAction {
					case "delete":
						actionCmd = s.deleteJobCmd(*s.selectedJob)
					case "pause":
						actionCmd = s.pauseJobCmd(*s.selectedJob)
					case "resume":
						actionCmd = s.resumeJobCmd(*s.selectedJob)
					case "run":
						actionCmd = s.runJobCmd(*s.selectedJob)
					}
				}
				s.viewState = s.actionSource
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
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
		return components.RenderError(s.err, s.Name(), "Jobs")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	return s.renderListView()
}

// renderConfirmation renders the job-delete confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.selectedJob == nil {
		return "Error: No job selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedJob.Name, "job")
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Jobs",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.jobs) == 0 {
		content.WriteString(components.EmptyState("jobs"))
	} else {
		content.WriteString(s.table.View())
	}
	return content.String()
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	name := s.createForm.Value("Name")
	region := s.createForm.Value("Region")
	schedule := s.createForm.Value("Schedule (cron)")
	uri := s.createForm.Value("Target URI")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateHTTPJob(s.projectID, region, name, schedule, uri); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Job %s created in %s", name, region)}
	}
}

// submitUpdateCmd fires the UpdateJobSchedule API call for the given job.
func (s *Service) submitUpdateCmd(job Job) tea.Cmd {
	schedule := s.updateForm.Value("Schedule (cron)")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateJobSchedule(s.projectID, job.Location, job.Name, schedule); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating schedule for job %s...", job.Name)}
	}
}

// deleteJobCmd triggers deletion of the given job
func (s *Service) deleteJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteJob(s.projectID, job.Location, job.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting job %s...", job.Name)}
	}
}

// pauseJobCmd triggers pausing the given job
func (s *Service) pauseJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.PauseJob(s.projectID, job.Location, job.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Pausing job %s...", job.Name)}
	}
}

// resumeJobCmd triggers resuming the given job
func (s *Service) resumeJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ResumeJob(s.projectID, job.Location, job.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resuming job %s...", job.Name)}
	}
}

// runJobCmd triggers an on-demand run of the given job
func (s *Service) runJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RunJob(s.projectID, job.Location, job.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Triggered job %s", job.Name)}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchJobsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("scheduler_jobs:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Job); ok {
					return jobsMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListJobs(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return jobsMsg(items)
	}
}

func (s *Service) updateTable(items []Job) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		nextRun := item.ScheduleTime
		if nextRun == "" {
			nextRun = "N/A"
		}
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.Schedule,
			item.TargetType,
			item.State,
			nextRun,
		}
	}
	s.table.SetRows(rows)
}

// getFilteredJobs returns filtered jobs based on the query string
func (s *Service) getFilteredJobs(jobs []Job, query string) []Job {
	if query == "" {
		return jobs
	}
	return components.FilterSlice(jobs, query, func(job Job, q string) bool {
		return components.ContainsMatch(job.Name, job.Location, job.Schedule, job.TargetType, job.State)(q)
	})
}
