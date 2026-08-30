package cloudtasks

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
	ViewIAM
	ViewIAMForm
	ViewTasks
	ViewTaskDetail
	ViewCreateTask
)

// newQueueUpdateForm builds the FormModel for updating a queue's max
// dispatch rate, seeded with its current value. Max concurrent dispatches,
// retry config, and app-engine routing overrides are out of scope.
func newQueueUpdateForm(q Queue) components.FormModel {
	return components.NewForm("Update Queue: "+q.Name, []components.FormField{
		{Label: "Max Dispatches/sec", Default: strconv.FormatFloat(q.MaxDispatchRate, 'f', -1, 64), Required: true, Validate: func(v string) string {
			n, err := strconv.ParseFloat(v, 64)
			if err != nil || n <= 0 {
				return "must be a positive number"
			}
			return ""
		}},
	})
}

// newCreateTaskForm builds the FormModel for creating a new HTTP-target task
// on q. App Engine-target tasks are deliberately out of scope.
func newCreateTaskForm(q Queue) components.FormModel {
	return components.NewForm("New Task on "+q.Name, []components.FormField{
		{Label: "URL", Placeholder: "https://example.com/handler", Required: true},
		{Label: "HTTP Method", Default: "POST", Placeholder: "POST"},
	})
}

type queuesMsg []Queue
type tasksMsg []Task
type errMsg error

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetQueueIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Queue]

	queues  []Queue
	spinner components.SpinnerModel
	err     error

	viewState     ViewState
	selectedQueue *Queue

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "pause", "resume", "purge", "grant", "run-task", "delete-task"
	actionSource  ViewState // Where to return after confirmation

	// IAM: current bindings for the selected queue, and the add-binding
	// form. pendingIAMRole/pendingIAMMember are captured at form-submit
	// time so the confirmation dialog and the actual API call use the same
	// values regardless of what the form fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Task list state (per selected queue) + create-task form + selection.
	tasks          []Task
	tasksTable     *components.StandardTable
	selectedTask   *Task
	createTaskForm components.FormModel

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Region", Width: 15},
		{Title: "State", Width: 12},
		{Title: "Max Rate/s", Width: 12},
		{Title: "Max Concurrent", Width: 16},
	}

	t := components.NewStandardTable(columns)

	taskColumns := []table.Column{
		{Title: "Task ID", Width: 30},
		{Title: "URL", Width: 40},
		{Title: "Method", Width: 8},
		{Title: "Schedule Time", Width: 20},
	}
	tt := components.NewStandardTable(taskColumns)

	svc := &Service{
		table:      t,
		tasksTable: tt,
		filter:     components.NewFilterWithPlaceholder("Filter queues..."),
		spinner:    components.NewSpinner(),
		viewState:  ViewList,
		cache:      cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredQueues, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud Tasks"
}

func (s *Service) ShortName() string {
	return "cloudtasks"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  n:New Queue  Ent:Detail"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  u:Update  p:Pause  R:Resume  x:Purge  d:Delete  i:IAM  T:Tasks"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewIAMForm || s.viewState == ViewCreateTask {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewIAM {
		return "a:Add Binding  q/Esc:Back"
	}
	if s.viewState == ViewTasks {
		return "r:Refresh  n:New Task  Ent:Details  R:Run  d:Delete  q/Esc:Back"
	}
	if s.viewState == ViewTaskDetail {
		return "R:Run  d:Delete  q/Esc:Back"
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
		s.fetchQueuesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedQueue = nil
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
		return s, tea.Batch(s.fetchQueuesCmd(false), s.tick())

	case queuesMsg:
		s.spinner.Stop()
		s.queues = msg
		s.filterSession.Apply(s.queues)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case iamPolicyMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.iamBindings = msg.bindings
		s.viewState = ViewIAM
		return s, nil

	case tasksMsg:
		s.spinner.Stop()
		s.tasks = msg
		s.updateTasksTable(s.tasks)
		s.viewState = ViewTasks
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedQueue != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedQueue),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "run-task" || s.pendingAction == "delete-task" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete-task" {
				s.selectedTask = nil
				s.viewState = ViewTasks
			}
			if s.selectedQueue != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchTasksCmd(*s.selectedQueue),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "create-task" {
			s.pendingAction = ""
			if msg.err != nil {
				s.createTaskForm.SubmitErr = msg.err.Error()
				s.viewState = ViewCreateTask
				return s, nil
			}
			if s.selectedQueue != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchTasksCmd(*s.selectedQueue),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "delete" || s.pendingAction == "pause" || s.pendingAction == "resume" || s.pendingAction == "purge" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				s.selectedQueue = nil
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
		if s.viewState == ViewUpdate {
			s.viewState = ViewDetail
		} else {
			s.viewState = ViewList
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
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedQueue != nil {
				return s, s.submitUpdateCmd(*s.selectedQueue)
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
				s.createForm = components.NewForm("New Queue", []components.FormField{
					{Label: "Queue ID", Placeholder: "my-queue", Required: true},
					{Label: "Region", Placeholder: "us-central1", Required: true},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				queues := s.getFilteredQueues(s.queues, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(queues) {
					s.selectedQueue = &queues[idx]
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
				s.selectedQueue = nil
				return s, nil
			case "u":
				if s.selectedQueue != nil {
					s.updateForm = newQueueUpdateForm(*s.selectedQueue)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "p": // Pause (Confirm)
				if s.selectedQueue != nil {
					s.pendingAction = "pause"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "R": // Resume (Confirm)
				if s.selectedQueue != nil {
					s.pendingAction = "resume"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x": // Purge (Confirm)
				if s.selectedQueue != nil {
					s.pendingAction = "purge"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if s.selectedQueue != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // View IAM bindings
				if s.selectedQueue != nil {
					return s, tea.Batch(s.fetchIAMCmd(*s.selectedQueue), s.spinner.Start(""))
				}
				return s, nil
			case "T": // View tasks
				if s.selectedQueue != nil {
					return s, tea.Batch(s.fetchTasksCmd(*s.selectedQueue), s.spinner.Start(""))
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAM {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "a":
				if s.selectedQueue != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedQueue.Name)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.selectedQueue != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewTasks {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				s.selectedTask = nil
				return s, nil
			case "r":
				if s.selectedQueue != nil {
					return s, tea.Batch(s.fetchTasksCmd(*s.selectedQueue), s.spinner.Start(""))
				}
				return s, nil
			case "n":
				if s.selectedQueue != nil {
					s.createTaskForm = newCreateTaskForm(*s.selectedQueue)
					s.viewState = ViewCreateTask
				}
				return s, nil
			case "enter":
				if idx := s.tasksTable.Cursor(); idx >= 0 && idx < len(s.tasks) {
					s.selectedTask = &s.tasks[idx]
					s.viewState = ViewTaskDetail
				}
				return s, nil
			case "R": // Run now (Confirm)
				if idx := s.tasksTable.Cursor(); idx >= 0 && idx < len(s.tasks) {
					s.selectedTask = &s.tasks[idx]
					s.pendingAction = "run-task"
					s.actionSource = ViewTasks
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if idx := s.tasksTable.Cursor(); idx >= 0 && idx < len(s.tasks) {
					s.selectedTask = &s.tasks[idx]
					s.pendingAction = "delete-task"
					s.actionSource = ViewTasks
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.tasksTable.Update(msg)
			s.tasksTable = updatedTable
			return s, cmd
		}

		if s.viewState == ViewTaskDetail {
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewTasks
				s.selectedTask = nil
				return s, nil
			case "R":
				if s.selectedTask != nil {
					s.pendingAction = "run-task"
					s.actionSource = ViewTaskDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d":
				if s.selectedTask != nil {
					s.pendingAction = "delete-task"
					s.actionSource = ViewTaskDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
			return s, nil
		}

		if s.viewState == ViewCreateTask {
			result, formCmd := s.createTaskForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewTasks
				return s, nil
			}
			if result.Submitted && s.selectedQueue != nil {
				s.pendingAction = "create-task"
				return s, s.submitCreateTaskCmd(*s.selectedQueue)
			}
			return s, formCmd
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "grant" && s.selectedQueue != nil {
					actionCmd = s.addIAMBindingCmd(*s.selectedQueue, s.pendingIAMRole, s.pendingIAMMember)
				} else if s.pendingAction == "run-task" && s.selectedQueue != nil && s.selectedTask != nil {
					actionCmd = s.runTaskCmd(*s.selectedQueue, *s.selectedTask)
				} else if s.pendingAction == "delete-task" && s.selectedQueue != nil && s.selectedTask != nil {
					actionCmd = s.deleteTaskCmd(*s.selectedQueue, *s.selectedTask)
				} else if s.selectedQueue != nil {
					switch s.pendingAction {
					case "delete":
						actionCmd = s.deleteQueueCmd(*s.selectedQueue)
					case "pause":
						actionCmd = s.pauseQueueCmd(*s.selectedQueue)
					case "resume":
						actionCmd = s.resumeQueueCmd(*s.selectedQueue)
					case "purge":
						actionCmd = s.purgeQueueCmd(*s.selectedQueue)
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
		return components.RenderError(s.err, s.Name(), "Queues")
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

	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}

	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}

	if s.viewState == ViewTasks {
		return s.renderTasksView()
	}

	if s.viewState == ViewTaskDetail {
		return s.renderTaskDetailView()
	}

	if s.viewState == ViewCreateTask {
		return s.createTaskForm.View()
	}

	return s.renderListView()
}

// renderConfirmation renders the queue-delete/IAM-grant/task confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "grant" {
		if s.selectedQueue == nil {
			return "Error: No queue selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedQueue.Name,
			"queue",
			components.IAMConfirmMessage("queue", s.selectedQueue.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	if s.pendingAction == "run-task" || s.pendingAction == "delete-task" {
		if s.selectedTask == nil {
			return "Error: No task selected"
		}
		action := "run"
		if s.pendingAction == "delete-task" {
			action = "delete"
		}
		return components.RenderConfirmation(action, s.selectedTask.Name, "task")
	}
	if s.selectedQueue == nil {
		return "Error: No queue selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedQueue.Name, "queue")
}

// renderIAMView renders the current IAM policy bindings for the selected
// queue, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedQueue == nil {
		return "Error: No queue selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Queues",
		s.selectedQueue.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedQueue.Name, rows)
}

// renderTasksView renders the list of tasks currently queued on the
// selected queue.
func (s *Service) renderTasksView() string {
	if s.selectedQueue == nil {
		return "Error: No queue selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Queues",
		s.selectedQueue.Name,
		"Tasks",
	)
	if len(s.tasks) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("tasks"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.tasksTable.View())
}

// renderTaskDetailView renders full details for a single task.
func (s *Service) renderTaskDetailView() string {
	if s.selectedQueue == nil || s.selectedTask == nil {
		return "Error: No task selected"
	}
	t := s.selectedTask
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Queues",
		s.selectedQueue.Name,
		"Tasks",
		t.Name,
	)
	card := components.DetailCard(components.DetailCardOpts{
		Title: "Task Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: t.Name},
			{Key: "URL", Value: t.URL},
			{Key: "Method", Value: t.HTTPMethod},
			{Key: "Schedule Time", Value: t.ScheduleTime},
			{Key: "Created", Value: t.CreateTime},
			{Key: "Dispatch Count", Value: fmt.Sprintf("%d", t.DispatchCnt)},
			{Key: "Response Count", Value: fmt.Sprintf("%d", t.ResponseCnt)},
		},
		FooterHint: "R Run  |  d Delete  |  q Back",
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Queues",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if len(s.queues) == 0 {
		content.WriteString(components.EmptyState("queues"))
	} else {
		content.WriteString(s.table.View())
	}
	return content.String()
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	queueID := s.createForm.Value("Queue ID")
	region := s.createForm.Value("Region")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateQueue(s.projectID, region, queueID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Queue %s created in %s", queueID, region)}
	}
}

// submitUpdateCmd fires the UpdateQueueMaxDispatchRate API call using the
// current update-form value.
func (s *Service) submitUpdateCmd(q Queue) tea.Cmd {
	rate := q.MaxDispatchRate
	if v := s.updateForm.Value("Max Dispatches/sec"); v != "" {
		if n, err := strconv.ParseFloat(v, 64); err == nil && n > 0 {
			rate = n
		}
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateQueueMaxDispatchRate(s.projectID, q.Location, q.Name, rate); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating queue %s...", q.Name)}
	}
}

// deleteQueueCmd triggers deletion of the given queue
func (s *Service) deleteQueueCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteQueue(s.projectID, q.Location, q.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting queue %s...", q.Name)}
	}
}

// pauseQueueCmd triggers pausing the given queue
func (s *Service) pauseQueueCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.PauseQueue(s.projectID, q.Location, q.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Pausing queue %s...", q.Name)}
	}
}

// resumeQueueCmd triggers resuming the given queue
func (s *Service) resumeQueueCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ResumeQueue(s.projectID, q.Location, q.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resuming queue %s...", q.Name)}
	}
}

// purgeQueueCmd triggers purging every task in the given queue
func (s *Service) purgeQueueCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.PurgeQueue(s.projectID, q.Location, q.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Purging queue %s...", q.Name)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a queue.
func (s *Service) fetchIAMCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetQueueIAMPolicy(s.projectID, q.Location, q.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given queue.
func (s *Service) addIAMBindingCmd(q Queue, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddQueueIAMBinding(s.projectID, q.Location, q.Name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on queue %s", role, member, q.Name)}
	}
}

// fetchTasksCmd lists the tasks currently queued on q.
func (s *Service) fetchTasksCmd(q Queue) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListTasks(s.projectID, q.Location, q.Name)
		if err != nil {
			return errMsg(err)
		}
		return tasksMsg(items)
	}
}

// submitCreateTaskCmd creates a new HTTP-target task on q from the
// create-task form's current values.
func (s *Service) submitCreateTaskCmd(q Queue) tea.Cmd {
	url := s.createTaskForm.Value("URL")
	method := s.createTaskForm.Value("HTTP Method")
	if method == "" {
		method = "POST"
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateHTTPTask(s.projectID, q.Location, q.Name, url, method); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Task created on queue %s", q.Name)}
	}
}

// runTaskCmd forces t to run now.
func (s *Service) runTaskCmd(q Queue, t Task) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RunTask(s.projectID, q.Location, q.Name, t.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Running task %s now", t.Name)}
	}
}

// deleteTaskCmd deletes t.
func (s *Service) deleteTaskCmd(q Queue, t Task) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteTask(s.projectID, q.Location, q.Name, t.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting task %s...", t.Name)}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchQueuesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("cloudtasks_queues:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Queue); ok {
					return queuesMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListQueues(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return queuesMsg(items)
	}
}

func (s *Service) updateTable(items []Queue) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.State,
			fmt.Sprintf("%.1f", item.MaxDispatchRate),
			fmt.Sprintf("%d", item.MaxConcurrent),
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateTasksTable(items []Task) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{item.Name, item.URL, item.HTTPMethod, item.ScheduleTime}
	}
	s.tasksTable.SetRows(rows)
}

// getFilteredQueues returns filtered queues based on the query string
func (s *Service) getFilteredQueues(queues []Queue, query string) []Queue {
	if query == "" {
		return queues
	}
	return components.FilterSlice(queues, query, func(queue Queue, q string) bool {
		return components.ContainsMatch(queue.Name, queue.Location, queue.State)(q)
	})
}
