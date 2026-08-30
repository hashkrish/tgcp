package dataflow

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

const CacheTTL = 30 * time.Second

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewConfirmation
	ViewUpdateOptions
)

// newUpdateOptionsForm builds the FormModel for updating a running
// Streaming Engine job's autoscaling bounds, matching `gcloud dataflow jobs
// update-options --min-num-workers --max-num-workers`.
func newUpdateOptionsForm(job Job) components.FormModel {
	return components.NewForm("Update Options: "+job.Name, []components.FormField{
		{Label: "Min Num Workers", Placeholder: "1 (blank = unchanged)"},
		{Label: "Max Num Workers", Placeholder: "10 (blank = unchanged)"},
	})
}

type jobsMsg []Job
type errMsg error

// actionResultMsg carries the result of an async action (e.g. launching a job)
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

	createForm        components.FormModel
	updateOptionsForm components.FormModel

	// Confirmation State
	pendingAction string    // "archive", "cancel", "drain"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Job Name", Width: 40},
		{Title: "Type", Width: 15},
		{Title: "State", Width: 15},
		{Title: "Location", Width: 10},
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
	return "Dataflow"
}

func (s *Service) ShortName() string {
	return "dataflow"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:Run Job"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdateOptions {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  c:Cancel  x:Drain  d:Archive  o:Update Options"
	}
	return "Esc/q:Back"
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
		if s.selectedJob != nil {
			for i := range s.jobs {
				if s.jobs[i].Name == s.selectedJob.Name {
					s.selectedJob = &s.jobs[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "archive" || s.pendingAction == "cancel" || s.pendingAction == "drain" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "archive" {
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
		if s.viewState == ViewUpdateOptions {
			if msg.err != nil {
				s.updateOptionsForm.SubmitErr = msg.err.Error()
				return s, nil
			}
			s.viewState = ViewDetail
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if msg.err != nil {
			s.createForm.SubmitErr = msg.err.Error()
			return s, nil
		}
		s.viewState = ViewList
		return s, tea.Batch(
			func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			},
			s.Refresh(),
		)

	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to table for click selection
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
				return s, s.launchTemplateJobCmd()
			}
			return s, formCmd
		}

		// Handle filter mode (only in list view)
		if s.viewState == ViewList {
			result := s.filterSession.HandleKey(msg)

			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
				// Continue processing other keys
			}
		}

		if s.viewState == ViewList {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("Run Dataflow Job", []components.FormField{
					{Label: "Job Name", Placeholder: "my-job", Required: true},
					{Label: "Region", Placeholder: "us-central1", Required: true},
					{Label: "Template GCS Path", Placeholder: "gs://bucket/templates/mytemplate", Required: true},
					{Label: "Parameters", Placeholder: "key=value,key2=value2"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				jobs := s.getFilteredJobs(s.jobs, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(jobs) {
					s.selectedJob = &jobs[idx]
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
				s.selectedJob = nil
				return s, nil
			case "c": // Cancel (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "cancel"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x": // Drain (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "drain"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Archive (Confirm)
				if s.selectedJob != nil {
					s.pendingAction = "archive"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "o": // Update autoscaling options
				if s.selectedJob != nil {
					s.updateOptionsForm = newUpdateOptionsForm(*s.selectedJob)
					s.viewState = ViewUpdateOptions
				}
				return s, nil
			}
		}

		if s.viewState == ViewUpdateOptions {
			result, formCmd := s.updateOptionsForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedJob != nil {
				minW, _ := strconv.ParseInt(s.updateOptionsForm.Value("Min Num Workers"), 10, 64)
				maxW, _ := strconv.ParseInt(s.updateOptionsForm.Value("Max Num Workers"), 10, 64)
				return s, s.updateJobOptionsCmd(*s.selectedJob, minW, maxW)
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.selectedJob != nil {
					switch s.pendingAction {
					case "archive":
						actionCmd = s.archiveJobCmd(*s.selectedJob)
					case "cancel":
						actionCmd = s.cancelJobCmd(*s.selectedJob)
					case "drain":
						actionCmd = s.drainJobCmd(*s.selectedJob)
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
// Data & Helpers
// -----------------------------------------------------------------------------

// launchTemplateJobCmd fires the LaunchTemplateJob API call using the current form values.
func (s *Service) launchTemplateJobCmd() tea.Cmd {
	jobName := s.createForm.Value("Job Name")
	region := s.createForm.Value("Region")
	gcsPath := s.createForm.Value("Template GCS Path")
	parameters := parseParameters(s.createForm.Value("Parameters"))
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.LaunchTemplateJob(s.projectID, region, jobName, gcsPath, parameters); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Job %s launched", jobName)}
	}
}

// archiveJobCmd triggers archiving of the given Dataflow job (Delete
// category — Dataflow has no true delete, only archive; see ArchiveJob).
func (s *Service) archiveJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ArchiveJob(s.projectID, job.Location, job.ID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Archiving job %s...", job.Name)}
	}
}

// cancelJobCmd triggers immediate cancellation of the given job
func (s *Service) cancelJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CancelJob(s.projectID, job.Location, job.ID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Cancelling job %s...", job.Name)}
	}
}

// drainJobCmd triggers a graceful drain of the given streaming job
func (s *Service) drainJobCmd(job Job) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DrainJob(s.projectID, job.Location, job.ID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Draining job %s...", job.Name)}
	}
}

// updateJobOptionsCmd updates job's autoscaling bounds.
func (s *Service) updateJobOptionsCmd(job Job, minWorkers, maxWorkers int64) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateJobOptions(s.projectID, job.Location, job.ID, minWorkers, maxWorkers); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updated autoscaling options for %s", job.Name)}
	}
}

// parseParameters parses a "key=value,key2=value2" free-text field into a
// map, ignoring malformed entries.
func parseParameters(raw string) map[string]string {
	if raw == "" {
		return nil
	}
	params := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		kv := strings.SplitN(strings.TrimSpace(pair), "=", 2)
		if len(kv) != 2 || kv[0] == "" {
			continue
		}
		params[kv[0]] = kv[1]
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

func (s *Service) fetchJobsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dataflow:%s", s.projectID)
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
		cleanState := strings.Replace(item.State, "JOB_STATE_", "", 1)
		cleanType := strings.Replace(item.Type, "JOB_TYPE_", "", 1)

		rows[i] = table.Row{
			item.Name,
			cleanType,
			cleanState,
			item.Location,
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
		return components.ContainsMatch(job.Name, job.Type, job.State, job.Location)(q)
	})
}
