package pubsub

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewListTopics ViewState = iota // Default
	ViewListSubs
	ViewDetailTopic
	ViewDetailSub
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewIAM
	ViewIAMForm
	ViewPublishForm
	ViewPulledMessages
	ViewModifyAckDeadlineForm
	ViewSeekForm
)

// newPublishForm builds the FormModel for publishing a single text message
// to a topic.
func newPublishForm(topic Topic) components.FormModel {
	return components.NewForm("Publish to Topic: "+topic.Name, []components.FormField{
		{Label: "Message", Placeholder: "message body", Required: true},
	})
}

// newModifyAckDeadlineForm builds the FormModel for extending/shortening the
// ack deadline on the currently-pulled messages of a subscription.
func newModifyAckDeadlineForm(sub Subscription) components.FormModel {
	return components.NewForm("Modify Ack Deadline: "+sub.Name, []components.FormField{
		{Label: "Ack Deadline Seconds", Placeholder: "60", Required: true, Validate: func(v string) string {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 600 {
				return "must be 0-600"
			}
			return ""
		}},
	})
}

// newSeekForm builds the FormModel for seeking a subscription's delivery
// cursor to a point in time, matching `gcloud pubsub subscriptions seek
// --time=TIMESTAMP`.
func newSeekForm(sub Subscription) components.FormModel {
	return components.NewForm("Seek Subscription: "+sub.Name, []components.FormField{
		{Label: "Time (RFC3339, or 'now')", Placeholder: "now", Default: "now", Required: true, Validate: func(v string) string {
			if v == "now" {
				return ""
			}
			if _, err := time.Parse(time.RFC3339, v); err != nil {
				return "must be RFC3339 (e.g. 2024-01-02T15:04:05Z) or 'now'"
			}
			return ""
		}},
	})
}

// newSubUpdateForm builds the FormModel for updating a subscription's ack
// deadline, seeded with its current value. Push config, retention, and
// dead-letter policy changes are out of scope for this minimal Update flow.
func newSubUpdateForm(sub Subscription) components.FormModel {
	return components.NewForm("Update Subscription: "+sub.Name, []components.FormField{
		{Label: "Ack Deadline Seconds", Default: strconv.Itoa(sub.AckDeadline), Required: true, Validate: func(v string) string {
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return "must be a positive integer"
			}
			return ""
		}},
	})
}

type topicsMsg []Topic
type subsMsg []Subscription
type errMsg error

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetTopicIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// publishResultMsg carries the result of a Publish call.
type publishResultMsg struct {
	messageID string
	err       error
}

// pulledMsg carries the result of a no-ack Pull call.
type pulledMsg struct {
	messages []PulledMessage
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter             components.FilterModel
	topicFilterSession components.FilterSession[Topic]
	subFilterSession   components.FilterSession[Subscription]

	topics []Topic
	subs   []Subscription

	spinner components.SpinnerModel
	err     error

	viewState     ViewState
	selectedTopic *Topic
	selectedSub   *Subscription

	// Create form state. createReturnView is where "n" was pressed from
	// (ViewListTopics or ViewListSubs), which decides both the field set
	// used to build the form and which list view Esc/success returns to.
	createForm       components.FormModel
	createReturnView ViewState

	// Update form state
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete", "grant", "publish", "ack", "modify-ack-deadline", "seek"
	actionSource  ViewState // Where to return after confirmation

	// IAM: current bindings for the selected topic or subscription, and the
	// add-binding form. iamResourceType/iamResourceName record which
	// resource ViewIAM/ViewIAMForm is currently scoped to ("topic" or
	// "subscription") since both share the same views. pendingIAMRole/
	// pendingIAMMember are captured at form-submit time so the confirmation
	// dialog and the actual API call use the same values regardless of what
	// the form fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	iamResourceType  string // "topic" or "subscription"
	iamResourceName  string
	pendingIAMRole   string
	pendingIAMMember string

	// Data-plane: publish form + pending message text (captured at
	// submit-time so the confirmation dialog and the actual Publish call
	// use the same value regardless of what the form field holds later),
	// and the last Pull result for read-only display.
	publishForm         components.FormModel
	pendingPublishText  string
	pulledMessages      []PulledMessage
	pulledMessagesTopic string // subscription name the pulled messages came from

	// Ack-deadline modification form + pending value.
	modifyAckDeadlineForm components.FormModel
	pendingAckDeadline    int64

	// Seek form + pending target time.
	seekForm     components.FormModel
	pendingSeekT string

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Default to Topic Columns
	columns := []table.Column{
		{Title: "Topic Name", Width: 40},
		{Title: "KMS Key", Width: 30},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter topics/subscriptions..."),
		spinner:   components.NewSpinner(),
		viewState: ViewListTopics,
		cache:     cache,
	}
	svc.topicFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredTopics, svc.updateTopicTable)
	svc.subFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredSubs, svc.updateSubTable)
	return svc
}

func (s *Service) Name() string {
	return "Pub/Sub"
}

func (s *Service) ShortName() string {
	return "pubsub"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewListTopics {
		return "r:Refresh  /:Filter  s:Switch to Subs  n:New Topic  Ent:Detail"
	}
	if s.viewState == ViewListSubs {
		return "r:Refresh  /:Filter  t:Switch to Topics  n:New Sub  Ent:Detail"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewState == ViewDetailSub {
		return "Esc/q:Back  u:Update  x:Detach  d:Delete  i:IAM  P:Pull (no ack)  S:Seek"
	}
	if s.viewState == ViewDetailTopic {
		return "Esc/q:Back  d:Delete  i:IAM  p:Publish"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewIAM {
		return "a:Add Binding  q/Esc:Back"
	}
	if s.viewState == ViewIAMForm || s.viewState == ViewPublishForm || s.viewState == ViewModifyAckDeadlineForm || s.viewState == ViewSeekForm {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewPulledMessages {
		return "Esc/q:Back  a:Ack All  m:Modify Ack Deadline"
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
		s.fetchTopicsCmd(true),
		s.fetchSubsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewListTopics
	s.selectedTopic = nil
	s.selectedSub = nil
	s.err = nil
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
	s.updateTopicTable(s.topics) // Reset cols
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewListTopics || s.viewState == ViewListSubs
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
		return s, tea.Batch(s.fetchTopicsCmd(false), s.fetchSubsCmd(false), s.tick())

	case topicsMsg:
		s.topics = msg
		if s.selectedTopic != nil {
			for i := range s.topics {
				if s.topics[i].Name == s.selectedTopic.Name {
					s.selectedTopic = &s.topics[i]
					break
				}
			}
		}
		if s.viewState == ViewListTopics {
			s.spinner.Stop()
			s.topicFilterSession.Apply(s.topics)
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case subsMsg:
		s.subs = msg
		if s.selectedSub != nil {
			for i := range s.subs {
				if s.subs[i].Name == s.selectedSub.Name {
					s.selectedSub = &s.subs[i]
					break
				}
			}
		}
		if s.viewState == ViewListSubs {
			s.spinner.Stop()
			s.subFilterSession.Apply(s.subs)
		}
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

	case publishResultMsg:
		s.pendingAction = ""
		s.viewState = s.actionSource
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		return s, func() tea.Msg {
			return core.ToastMsg{Message: fmt.Sprintf("Published message %s", msg.messageID), Type: core.ToastSuccess}
		}

	case pulledMsg:
		s.spinner.Stop()
		if msg.err != nil {
			s.viewState = ViewDetailSub
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.pulledMessages = msg.messages
		if s.selectedSub != nil {
			s.pulledMessagesTopic = s.selectedSub.Name
		}
		s.viewState = ViewPulledMessages
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.iamResourceName != "" {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(s.iamResourceType, s.iamResourceName),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "ack" {
			s.pendingAction = ""
			s.viewState = ViewDetailSub
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			s.pulledMessages = nil
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "modify-ack-deadline" {
			s.pendingAction = ""
			s.viewState = ViewPulledMessages
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "seek" {
			s.pendingAction = ""
			s.viewState = ViewDetailSub
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "delete" || s.pendingAction == "detach" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				if s.viewState == ViewDetailTopic {
					s.viewState = ViewListTopics
					s.selectedTopic = nil
				} else {
					s.viewState = ViewListSubs
					s.selectedSub = nil
				}
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
			s.viewState = ViewDetailSub
		} else {
			s.viewState = s.createReturnView
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
		// Forward mouse events to table for click selection
		if s.viewState == ViewListTopics || s.viewState == ViewListSubs {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.createReturnView
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
				s.viewState = ViewDetailSub
				return s, nil
			}
			if result.Submitted && s.selectedSub != nil {
				return s, s.updateSubCmd(*s.selectedSub)
			}
			return s, formCmd
		}

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.iamResourceName != "" {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewModifyAckDeadlineForm {
			result, formCmd := s.modifyAckDeadlineForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewPulledMessages
				return s, nil
			}
			if result.Submitted && s.selectedSub != nil {
				n, _ := strconv.Atoi(s.modifyAckDeadlineForm.Value("Ack Deadline Seconds"))
				s.pendingAckDeadline = int64(n)
				s.pendingAction = "modify-ack-deadline"
				s.actionSource = ViewPulledMessages
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewSeekForm {
			result, formCmd := s.seekForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetailSub
				return s, nil
			}
			if result.Submitted && s.selectedSub != nil {
				s.pendingSeekT = s.seekForm.Value("Time (RFC3339, or 'now')")
				s.pendingAction = "seek"
				s.actionSource = ViewDetailSub
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewPublishForm {
			result, formCmd := s.publishForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetailTopic
				return s, nil
			}
			if result.Submitted && s.selectedTopic != nil {
				s.pendingPublishText = s.publishForm.Value("Message")
				s.pendingAction = "publish"
				s.actionSource = ViewDetailTopic
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewPulledMessages {
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewDetailSub
			case "a": // Ack all pulled messages
				if s.selectedSub != nil && len(s.pulledMessages) > 0 {
					s.pendingAction = "ack"
					s.actionSource = ViewPulledMessages
					s.viewState = ViewConfirmation
				}
			case "m": // Modify ack deadline of all pulled messages
				if s.selectedSub != nil && len(s.pulledMessages) > 0 {
					s.modifyAckDeadlineForm = newModifyAckDeadlineForm(*s.selectedSub)
					s.viewState = ViewModifyAckDeadlineForm
				}
			}
			return s, nil
		}

		// Handle filter mode (only in list views)
		if s.viewState == ViewListTopics || s.viewState == ViewListSubs {
			var result components.FilterUpdateResult
			if s.viewState == ViewListTopics {
				result = s.topicFilterSession.HandleKey(msg)
			} else {
				result = s.subFilterSession.HandleKey(msg)
			}

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

		// Root Views (Topics or Subs)
		if s.viewState == ViewListTopics || s.viewState == ViewListSubs {
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n": // New topic/subscription (create form)
				s.createReturnView = s.viewState
				if s.viewState == ViewListTopics {
					s.createForm = components.NewForm("New Topic", []components.FormField{
						{Label: "Topic ID", Placeholder: "my-topic", Required: true},
					})
				} else {
					s.createForm = components.NewForm("New Subscription", []components.FormField{
						{Label: "Subscription ID", Placeholder: "my-subscription", Required: true},
						{Label: "Topic", Placeholder: "existing-topic-name", Required: true},
						{Label: "Ack Deadline Seconds", Default: "10"},
					})
				}
				s.viewState = ViewCreate
				return s, nil
			case "s": // Switch to Subs
				if s.viewState == ViewListTopics {
					s.viewState = ViewListSubs
					s.filter.ExitFilterMode() // Clear topic filter so it doesn't leak into subs
					s.table.SetCursor(0)
					s.subFilterSession.Apply(s.subs) // Render existing if available
					if len(s.subs) == 0 {
						return s, tea.Batch(s.fetchSubsCmd(true), s.spinner.Start(""))
					}
				}
			case "t": // Switch to Topics
				if s.viewState == ViewListSubs {
					s.viewState = ViewListTopics
					s.filter.ExitFilterMode() // Clear sub filter so it doesn't leak into topics
					s.table.SetCursor(0)
					s.topicFilterSession.Apply(s.topics)
					if len(s.topics) == 0 {
						return s, tea.Batch(s.fetchTopicsCmd(true), s.spinner.Start(""))
					}
				}
			case "enter":
				if s.viewState == ViewListTopics {
					topics := s.getFilteredTopics(s.topics, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(topics) {
						s.selectedTopic = &topics[idx]
						s.viewState = ViewDetailTopic
					}
				} else {
					subs := s.getFilteredSubs(s.subs, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(subs) {
						s.selectedSub = &subs[idx]
						s.viewState = ViewDetailSub
					}
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

		// Detail Views
		if s.viewState == ViewDetailTopic || s.viewState == ViewDetailSub {
			switch msg.String() {
			case "esc", "q":
				if s.viewState == ViewDetailTopic {
					s.viewState = ViewListTopics
					s.selectedTopic = nil
				} else {
					s.viewState = ViewListSubs
					s.selectedSub = nil
				}
				return s, nil
			case "u":
				if s.viewState == ViewDetailSub && s.selectedSub != nil {
					s.updateForm = newSubUpdateForm(*s.selectedSub)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "x": // Detach (Confirm) — subscriptions only
				if s.viewState == ViewDetailSub && s.selectedSub != nil {
					s.pendingAction = "detach"
					s.actionSource = s.viewState
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d": // Delete (Confirm)
				if (s.viewState == ViewDetailTopic && s.selectedTopic != nil) ||
					(s.viewState == ViewDetailSub && s.selectedSub != nil) {
					s.pendingAction = "delete"
					s.actionSource = s.viewState
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // View IAM bindings — topics and subscriptions
				if s.viewState == ViewDetailTopic && s.selectedTopic != nil {
					s.iamResourceType = "topic"
					s.iamResourceName = s.selectedTopic.Name
					return s, tea.Batch(s.fetchIAMCmd(s.iamResourceType, s.iamResourceName), s.spinner.Start(""))
				}
				if s.viewState == ViewDetailSub && s.selectedSub != nil {
					s.iamResourceType = "subscription"
					s.iamResourceName = s.selectedSub.Name
					return s, tea.Batch(s.fetchIAMCmd(s.iamResourceType, s.iamResourceName), s.spinner.Start(""))
				}
				return s, nil
			case "p": // Publish message — topics only
				if s.viewState == ViewDetailTopic && s.selectedTopic != nil {
					s.publishForm = newPublishForm(*s.selectedTopic)
					s.viewState = ViewPublishForm
				}
				return s, nil
			case "P": // Pull without ack — subscriptions only
				if s.viewState == ViewDetailSub && s.selectedSub != nil {
					return s, tea.Batch(s.pullCmd(*s.selectedSub), s.spinner.Start(""))
				}
				return s, nil
			case "S": // Seek — subscriptions only
				if s.viewState == ViewDetailSub && s.selectedSub != nil {
					s.seekForm = newSeekForm(*s.selectedSub)
					s.viewState = ViewSeekForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewIAM {
			switch msg.String() {
			case "q", "esc":
				if s.iamResourceType == "subscription" {
					s.viewState = ViewDetailSub
				} else {
					s.viewState = ViewDetailTopic
				}
				return s, nil
			case "a":
				if s.iamResourceName != "" {
					s.iamForm = components.NewIAMAddBindingForm(s.iamResourceName)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" {
					if s.actionSource == ViewDetailTopic && s.selectedTopic != nil {
						actionCmd = s.deleteTopicCmd(*s.selectedTopic)
					} else if s.actionSource == ViewDetailSub && s.selectedSub != nil {
						actionCmd = s.deleteSubCmd(*s.selectedSub)
					}
				} else if s.pendingAction == "detach" && s.selectedSub != nil {
					actionCmd = s.detachSubCmd(*s.selectedSub)
				} else if s.pendingAction == "grant" && s.iamResourceName != "" {
					actionCmd = s.addIAMBindingCmd(s.iamResourceType, s.iamResourceName, s.pendingIAMRole, s.pendingIAMMember)
				} else if s.pendingAction == "publish" && s.selectedTopic != nil {
					actionCmd = s.publishCmd(*s.selectedTopic, s.pendingPublishText)
				} else if s.pendingAction == "ack" && s.selectedSub != nil {
					actionCmd = s.ackCmd(*s.selectedSub, s.pulledAckIDs())
				} else if s.pendingAction == "modify-ack-deadline" && s.selectedSub != nil {
					actionCmd = s.modifyAckDeadlineCmd(*s.selectedSub, s.pulledAckIDs(), s.pendingAckDeadline)
				} else if s.pendingAction == "seek" && s.selectedSub != nil {
					actionCmd = s.seekCmd(*s.selectedSub, s.pendingSeekT)
				}
				// Stay on the originating detail view (and keep
				// pendingAction "delete") until actionResultMsg arrives —
				// it decides whether to pop back to the list or surface
				// an error without losing the current selection.
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
// Create
// -----------------------------------------------------------------------------

// submitCreateCmd fires the appropriate create API call based on which list
// view the create form was opened from.
func (s *Service) submitCreateCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if s.createReturnView == ViewListTopics {
			topicID := s.createForm.Value("Topic ID")
			if err := s.client.CreateTopic(s.projectID, topicID); err != nil {
				return actionResultMsg{err: err}
			}
			return actionResultMsg{msg: fmt.Sprintf("Topic %s created", topicID)}
		}

		subID := s.createForm.Value("Subscription ID")
		topicID := s.createForm.Value("Topic")
		ackDeadline := 10
		if v := s.createForm.Value("Ack Deadline Seconds"); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				ackDeadline = n
			}
		}
		if err := s.client.CreateSubscription(s.projectID, subID, topicID, ackDeadline); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Subscription %s created", subID)}
	}
}

// updateSubCmd fires the UpdateSubscriptionAckDeadline API call using the
// current update-form value.
func (s *Service) updateSubCmd(sub Subscription) tea.Cmd {
	ackDeadline := sub.AckDeadline
	if v := s.updateForm.Value("Ack Deadline Seconds"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			ackDeadline = n
		}
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateSubscriptionAckDeadline(s.projectID, sub.Name, ackDeadline); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating subscription %s...", sub.Name)}
	}
}

// deleteTopicCmd triggers deletion of the given topic
func (s *Service) deleteTopicCmd(topic Topic) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteTopic(s.projectID, topic.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting topic %s...", topic.Name)}
	}
}

// deleteSubCmd triggers deletion of the given subscription
func (s *Service) deleteSubCmd(sub Subscription) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteSubscription(s.projectID, sub.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting subscription %s...", sub.Name)}
	}
}

// detachSubCmd triggers detaching the given subscription from its topic
func (s *Service) detachSubCmd(sub Subscription) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DetachSubscription(s.projectID, sub.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Detaching subscription %s...", sub.Name)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a topic or subscription,
// dispatching on resourceType ("topic" or "subscription").
func (s *Service) fetchIAMCmd(resourceType, name string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		var (
			bindings []IAMBinding
			err      error
		)
		if resourceType == "subscription" {
			bindings, err = s.client.GetSubscriptionIAMPolicy(s.projectID, name)
		} else {
			bindings, err = s.client.GetTopicIAMPolicy(s.projectID, name)
		}
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given topic or subscription.
func (s *Service) addIAMBindingCmd(resourceType, name, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		var err error
		if resourceType == "subscription" {
			err = s.client.AddSubscriptionIAMBinding(s.projectID, name, role, member)
		} else {
			err = s.client.AddTopicIAMBinding(s.projectID, name, role, member)
		}
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on %s %s", role, member, resourceType, name)}
	}
}

// pulledAckIDs returns the ack IDs of the currently-displayed Pull result.
func (s *Service) pulledAckIDs() []string {
	ids := make([]string, 0, len(s.pulledMessages))
	for _, m := range s.pulledMessages {
		if m.AckID != "" {
			ids = append(ids, m.AckID)
		}
	}
	return ids
}

// ackCmd acknowledges every currently-pulled message on sub.
func (s *Service) ackCmd(sub Subscription, ackIDs []string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.Ack(s.projectID, sub.Name, ackIDs); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Acknowledged %d message(s) on %s", len(ackIDs), sub.Name)}
	}
}

// modifyAckDeadlineCmd extends/shortens the ack deadline of every
// currently-pulled message on sub.
func (s *Service) modifyAckDeadlineCmd(sub Subscription, ackIDs []string, deadline int64) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.ModifyAckDeadline(s.projectID, sub.Name, ackIDs, deadline); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Set ack deadline to %ds for %d message(s)", deadline, len(ackIDs))}
	}
}

// seekCmd resets sub's delivery cursor to the given time ("now" or RFC3339).
func (s *Service) seekCmd(sub Subscription, targetTime string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		t := time.Now()
		if targetTime != "now" {
			parsed, err := time.Parse(time.RFC3339, targetTime)
			if err != nil {
				return actionResultMsg{err: fmt.Errorf("invalid time: %w", err)}
			}
			t = parsed
		}
		if err := s.client.SeekToTime(s.projectID, sub.Name, t); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Seeked %s to %s", sub.Name, t.Format(time.RFC3339))}
	}
}

// publishCmd publishes message to the given topic.
func (s *Service) publishCmd(topic Topic, message string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return publishResultMsg{err: fmt.Errorf("client not initialized")}
		}
		id, err := s.client.Publish(s.projectID, topic.Name, message)
		if err != nil {
			return publishResultMsg{err: err}
		}
		return publishResultMsg{messageID: id}
	}
}

// pullCmd pulls up to a handful of currently-available messages from the
// given subscription WITHOUT acknowledging them.
func (s *Service) pullCmd(sub Subscription) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return pulledMsg{err: fmt.Errorf("client not initialized")}
		}
		messages, err := s.client.Pull(s.projectID, sub.Name, 10)
		if err != nil {
			return pulledMsg{err: err}
		}
		return pulledMsg{messages: messages}
	}
}

// -----------------------------------------------------------------------------
// Data Fetching
// -----------------------------------------------------------------------------

func (s *Service) fetchTopicsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("pubsub_topics:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Topic); ok {
					return topicsMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListTopics(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return topicsMsg(items)
	}
}

func (s *Service) fetchSubsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("pubsub_subs:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Subscription); ok {
					return subsMsg(items)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}
		items, err := s.client.ListSubscriptions(s.projectID)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}
		return subsMsg(items)
	}
}

// -----------------------------------------------------------------------------
// Table Updates
// -----------------------------------------------------------------------------

func (s *Service) updateTopicTable(items []Topic) {
	// Reconfigure cols for Topics
	columns := []table.Column{
		{Title: "Topic Name (Press 's' for Subs)", Width: 40},
		{Title: "KMS Key", Width: 30},
	}
	// Clear rows first to prevent panic on column resize
	s.table.SetRows([]table.Row{})
	s.table.SetColumns(columns)

	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{item.Name, item.KmsKeyName}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateSubTable(items []Subscription) {
	// Reconfigure cols for Subs
	columns := []table.Column{
		{Title: "Subscription (Press 't' for Topics)", Width: 35},
		{Title: "Topic", Width: 25},
		{Title: "Type", Width: 10},
		{Title: "Ack Deadline", Width: 15},
	}
	// Clear rows first to prevent panic on column resize
	s.table.SetRows([]table.Row{})
	s.table.SetColumns(columns)

	rows := make([]table.Row, len(items))
	for i, item := range items {
		subType := "Pull"
		if item.PushEndpoint != "" {
			subType = "Push"
		}
		if item.DeadLetterTopic != "" {
			subType += " (DLQ)"
		}

		rows[i] = table.Row{
			item.Name,
			item.Topic,
			subType,
			fmt.Sprintf("%d sec", item.AckDeadline),
		}
	}
	s.table.SetRows(rows)
}

// getFilteredTopics returns filtered topics based on the query string
func (s *Service) getFilteredTopics(topics []Topic, query string) []Topic {
	if query == "" {
		return topics
	}
	return components.FilterSlice(topics, query, func(topic Topic, q string) bool {
		return components.ContainsMatch(topic.Name, topic.KmsKeyName)(q)
	})
}

// getFilteredSubs returns filtered subscriptions based on the query string
func (s *Service) getFilteredSubs(subs []Subscription, query string) []Subscription {
	if query == "" {
		return subs
	}
	return components.FilterSlice(subs, query, func(sub Subscription, q string) bool {
		return components.ContainsMatch(sub.Name, sub.Topic, sub.PushEndpoint)(q)
	})
}
