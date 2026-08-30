package gcs

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 5 * time.Minute // Buckets don't change often

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewObjects
	ViewObjectDetail
	ViewConfirmation
	ViewCreate
	ViewUpdate
	ViewIAM
	ViewIAMForm
	ViewMoveObject
	ViewSignURL
)

// newBucketUpdateForm builds the FormModel for updating a bucket's default
// storage class, versioning, retention period, a single CORS rule, and a
// single age-based lifecycle delete rule, seeded with current values where
// applicable. Bucket relocate and full multi-rule lifecycle/CORS configs
// are out of scope for this minimal Update flow.
func newBucketUpdateForm(b Bucket) components.FormModel {
	retentionDays := "0"
	if b.RetentionPeriod > 0 {
		retentionDays = strconv.FormatInt(int64(b.RetentionPeriod/(24*time.Hour)), 10)
	}
	return components.NewForm("Update Bucket: "+b.Name, []components.FormField{
		{Label: "Storage Class", Default: b.StorageClass, Placeholder: "STANDARD", Required: true},
		{Label: "Versioning Enabled", Default: strconv.FormatBool(b.VersioningEnabled), Placeholder: "true/false", Required: true, Validate: validateBoolField},
		{Label: "Retention Days", Default: retentionDays, Placeholder: "0 = disabled", Validate: validateNonNegativeIntField},
		{Label: "CORS Origins", Placeholder: "https://example.com,https://foo.com (comma-separated, blank = leave CORS unchanged)"},
		{Label: "CORS Methods", Placeholder: "GET,POST (comma-separated)"},
		{Label: "Lifecycle Delete After Days", Placeholder: "0 = disabled", Validate: validateNonNegativeIntField},
	})
}

func validateBoolField(v string) string {
	if _, err := strconv.ParseBool(v); err != nil {
		return "must be true or false"
	}
	return ""
}

func validateNonNegativeIntField(v string) string {
	if v == "" {
		return ""
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return "must be a non-negative integer"
	}
	return ""
}

// newMoveObjectForm builds the FormModel for renaming/moving a single
// object within its bucket, seeded with its current name.
func newMoveObjectForm(obj Object) components.FormModel {
	return components.NewForm("Move Object: "+obj.Name, []components.FormField{
		{Label: "New Name", Default: obj.Name, Placeholder: "path/to/new-name.txt", Required: true},
	})
}

// signURLExpiryPattern matches a plain integer count of minutes.
var signURLExpiryPattern = regexp.MustCompile(`^[0-9]+$`)

// newSignURLForm builds the FormModel for generating a signed GET URL for
// an object, matching `gcloud storage sign-url --duration`.
func newSignURLForm(obj Object) components.FormModel {
	return components.NewForm("Sign URL: "+obj.Name, []components.FormField{
		{Label: "Service Account Email", Placeholder: "my-sa@project.iam.gserviceaccount.com", Required: true},
		{Label: "Expiry (minutes)", Default: "60", Required: true, Validate: func(v string) string {
			if !signURLExpiryPattern.MatchString(v) {
				return "must be a whole number of minutes"
			}
			return ""
		}},
	})
}

// bucketsMsg is the message used to pass fetched data
type bucketsMsg []Bucket

type objectsMsg []Object

// errMsg is the standard error message
type errMsg error

// actionResultMsg carries the result of an async action (e.g. bucket creation)
type actionResultMsg struct {
	err error
	msg string
}

// downloadResultMsg carries the result of downloading an object to local
// disk (the "cp" data-plane action).
type downloadResultMsg struct {
	err  error
	path string
}

// iamPolicyMsg carries the result of a GetBucketIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

// Service implements the services.Service interface
type Service struct {
	client      *Client
	projectID   string
	table       *components.StandardTable
	objectTable *components.StandardTable

	// UI Components
	filter              components.FilterModel
	bucketFilterSession components.FilterSession[Bucket]
	objectFilterSession components.FilterSession[Object]
	spinner             components.SpinnerModel

	// State
	buckets []Bucket
	objects []Object
	err     error

	// View State
	viewState      ViewState
	selectedBucket *Bucket
	selectedObject *Object
	currentPrefix  string

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// Create form
	createForm components.FormModel

	// Update form
	updateForm components.FormModel

	// IAM: current bindings for the selected bucket, and the add-binding
	// form. pendingIAMRole/pendingIAMMember are captured at form-submit time
	// so the confirmation dialog and the actual API call use the same
	// values regardless of what the form fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Move (mv): the destination-name form for the currently selected object.
	moveForm components.FormModel

	// Sign URL: the service-account/expiry form for the currently selected object.
	signURLForm components.FormModel

	// Cache
	cache *core.Cache
}

// NewService creates a new instance of the service
func NewService(cache *core.Cache) *Service {
	// 1. Table Setup
	columns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Location", Width: 15},
		{Title: "Class", Width: 15},
		{Title: "Created", Width: 20},
	}

	t := components.NewStandardTable(columns)

	// 1b. Object Table Setup
	objColumns := []table.Column{
		{Title: "Name", Width: 40},
		{Title: "Type", Width: 15},
		{Title: "Size", Width: 10},
		{Title: "Updated", Width: 15},
	}
	ot := components.NewStandardTable(objColumns)

	svc := &Service{
		table:       t,
		objectTable: ot,
		filter:      components.NewFilterWithPlaceholder("Filter buckets..."),
		spinner:     components.NewSpinner(),
		viewState:   ViewList,
		cache:       cache,
	}
	svc.bucketFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredBuckets, svc.updateTable)
	svc.objectFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredObjects, svc.updateObjectTable)
	return svc
}

// Name returns the full human-readable name
func (s *Service) Name() string {
	return "Cloud Storage"
}

// ShortName returns the specialized identifier
func (s *Service) ShortName() string {
	return "gcs"
}

// HelpText returns context-aware keybindings
func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:New Bucket"
	}
	if s.viewState == ViewCreate || s.viewState == ViewUpdate || s.viewState == ViewMoveObject || s.viewState == ViewSignURL {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	if s.viewState == ViewDetail {
		return "Enter:Browse Objects  Esc/q:Back  u:Update  d:Delete  i:IAM"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewObjects {
		return "Enter:Open  d:Delete  c:Download  m:Move  g:Sign URL  Esc/q:Back/Up"
	}
	if s.viewState == ViewObjectDetail {
		return "Esc/q:Back"
	}
	if s.viewState == ViewIAM {
		return "a:Add Binding  q/Esc:Back"
	}
	if s.viewState == ViewIAMForm {
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Lifecycle & Interface Implementation
// -----------------------------------------------------------------------------

// InitService initializes the API client
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

// Init startup commands
func (s *Service) Init() tea.Cmd {
	return s.tick()
}

// tick creates a background ticker for cache invalidation
func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Refresh triggers a forced data reload
func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""), // Start animated spinner
		s.fetchBucketsCmd(true),
	)
}

// Reset clears the service state when navigating away or switching projects
func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedBucket = nil
	s.err = nil          // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0) // Reset table position
	s.currentPrefix = ""
	s.selectedObject = nil
	s.filter.ExitFilterMode()
}

// IsRootView returns true if we are at the top-level list
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// Focus handles input focus (Visual Highlight)
func (s *Service) Focus() {
	s.table.Focus()
	s.objectTable.Focus()
}

// Blur handles loss of input focus (Visual Dimming)
func (s *Service) Blur() {
	s.table.Blur()
	s.objectTable.Blur()
}

// -----------------------------------------------------------------------------
// Update Loop
// -----------------------------------------------------------------------------

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	// Spinner Animation
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	// 1. Background Tick
	case tickMsg:
		return s, tea.Batch(s.fetchBucketsCmd(false), s.tick())

	// 2. Data Loaded
	case bucketsMsg:
		s.spinner.Stop()
		s.buckets = msg
		if s.selectedBucket != nil {
			for i := range s.buckets {
				if s.buckets[i].Name == s.selectedBucket.Name {
					s.selectedBucket = &s.buckets[i]
					break
				}
			}
		}
		s.bucketFilterSession.Apply(s.buckets)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case objectsMsg:
		s.spinner.Stop()
		s.objects = msg
		s.objectFilterSession.Apply(s.objects)
		s.objectTable.SetCursor(0)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	// 3. Error Handling
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

	case downloadResultMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		return s, func() tea.Msg {
			return core.ToastMsg{Message: fmt.Sprintf("Downloaded to %s", msg.path), Type: core.ToastSuccess}
		}

	case actionResultMsg:
		if s.pendingAction == "delete-object" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedObject != nil {
				s.objects = removeObjectByName(s.objects, s.selectedObject.Name)
				s.objectFilterSession.Apply(s.objects)
			}
			s.selectedObject = nil
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			}
		}
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedBucket != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedBucket),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			s.selectedBucket = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.pendingAction == "move-object" {
			s.pendingAction = ""
			if msg.err != nil {
				s.moveForm.SubmitErr = msg.err.Error()
				s.viewState = ViewMoveObject
				return s, nil
			}
			s.selectedObject = nil
			s.viewState = ViewObjects
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.fetchObjectsCmd(),
			)
		}
		if s.pendingAction == "sign-url" {
			s.pendingAction = ""
			if msg.err != nil {
				s.signURLForm.SubmitErr = msg.err.Error()
				s.viewState = ViewSignURL
				return s, nil
			}
			s.selectedObject = nil
			s.viewState = ViewObjects
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
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
		return s, tea.Batch(
			func() tea.Msg {
				return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
			},
			s.Refresh(),
		)

	// 4. Window Resize
	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)
		s.objectTable.HandleWindowSizeDefault(msg)

	// 4.5 Mouse Input
	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		switch s.viewState {
		case ViewList:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		case ViewObjects:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.objectTable.Update(msg)
			s.objectTable = updatedTable
			return s, cmd
		}

	// 5. User Input
	case tea.KeyMsg:
		if s.viewState == ViewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				return s, s.createBucketCmd()
			}
			return s, formCmd
		}

		if s.viewState == ViewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewDetail
				return s, nil
			}
			if result.Submitted && s.selectedBucket != nil {
				return s, s.updateBucketCmd(*s.selectedBucket)
			}
			return s, formCmd
		}

		if s.viewState == ViewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewIAM
				return s, nil
			}
			if result.Submitted && s.selectedBucket != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.actionSource = ViewIAM
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, formCmd
		}

		if s.viewState == ViewMoveObject {
			result, formCmd := s.moveForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewObjects
				s.selectedObject = nil
				return s, nil
			}
			if result.Submitted && s.selectedBucket != nil && s.selectedObject != nil {
				s.pendingAction = "move-object"
				return s, tea.Batch(s.moveObjectCmd(*s.selectedBucket, *s.selectedObject, s.moveForm.Value("New Name")), s.spinner.Start(""))
			}
			return s, formCmd
		}

		if s.viewState == ViewSignURL {
			result, formCmd := s.signURLForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewObjects
				s.selectedObject = nil
				return s, nil
			}
			if result.Submitted && s.selectedBucket != nil && s.selectedObject != nil {
				s.pendingAction = "sign-url"
				minutes, _ := strconv.Atoi(s.signURLForm.Value("Expiry (minutes)"))
				expiry := time.Duration(minutes) * time.Minute
				return s, tea.Batch(
					s.signURLCmd(*s.selectedBucket, *s.selectedObject, s.signURLForm.Value("Service Account Email"), expiry),
					s.spinner.Start(""),
				)
			}
			return s, formCmd
		}

		// Handle filter mode (list or object view)
		if s.viewState == ViewList || s.viewState == ViewObjects {
			var result components.FilterUpdateResult
			if s.viewState == ViewList {
				result = s.bucketFilterSession.HandleKey(msg)
			} else {
				result = s.objectFilterSession.HandleKey(msg)
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

		switch s.viewState {
		case ViewList:
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "n":
				s.createForm = components.NewForm("Create Bucket", []components.FormField{
					{Label: "Name", Placeholder: "my-new-bucket", Required: true},
					{Label: "Location", Placeholder: "US", Default: "US"},
					{Label: "Storage Class", Placeholder: "STANDARD", Default: "STANDARD"},
				})
				s.viewState = ViewCreate
				return s, nil
			case "enter":
				// Handle bucket selection -> Go to Details
				if s.selectedBucket == nil {
					buckets := s.getFilteredBuckets(s.buckets, s.filter.Value())
					if idx := s.table.Cursor(); idx >= 0 && idx < len(buckets) {
						s.selectedBucket = &buckets[idx]
						s.viewState = ViewDetail
					}
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd

		case ViewDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewList
				s.selectedBucket = nil
				s.bucketFilterSession.Apply(s.buckets)
				return s, nil
			case "enter":
				// Go to Object Browser
				s.filter.ExitFilterMode() // Clear bucket filter so it doesn't leak into the object list
				s.viewState = ViewObjects
				s.currentPrefix = ""
				return s, tea.Batch(s.fetchObjectsCmd(), s.spinner.Start(""))
			case "u":
				if s.selectedBucket != nil {
					s.updateForm = newBucketUpdateForm(*s.selectedBucket)
					s.viewState = ViewUpdate
				}
				return s, nil
			case "d": // Delete (Confirm) — empty-bucket check happens before the call fires
				if s.selectedBucket != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "i": // View IAM bindings
				if s.selectedBucket != nil {
					return s, tea.Batch(s.fetchIAMCmd(*s.selectedBucket), s.spinner.Start(""))
				}
				return s, nil
			}

		case ViewIAM:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewDetail
				return s, nil
			case "a":
				if s.selectedBucket != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedBucket.Name)
					s.viewState = ViewIAMForm
				}
				return s, nil
			}

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedBucket != nil {
					actionCmd = s.DeleteBucketCmd(*s.selectedBucket)
				} else if s.pendingAction == "delete-object" && s.selectedBucket != nil && s.selectedObject != nil {
					actionCmd = s.deleteObjectCmd(*s.selectedBucket, *s.selectedObject)
				} else if s.pendingAction == "grant" && s.selectedBucket != nil {
					actionCmd = s.addIAMBindingCmd(*s.selectedBucket, s.pendingIAMRole, s.pendingIAMMember)
				}
				// Stay on pendingAction until actionResultMsg arrives — it
				// decides whether to show a toast and pop back to the
				// list/IAM view, or surface an error without losing the
				// current selection.
				s.viewState = s.actionSource
				return s, actionCmd
			case "n", "esc", "q":
				if s.pendingAction == "delete-object" {
					s.selectedObject = nil
				}
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}

		case ViewObjects:
			switch msg.String() {
			case "esc", "q":
				if s.currentPrefix == "" {
					s.filter.ExitFilterMode() // Clear object filter so it doesn't leak into the bucket list
					s.viewState = ViewDetail  // Back to Details
				} else {
					s.currentPrefix = parentPrefix(s.currentPrefix)
					return s, tea.Batch(s.fetchObjectsCmd(), s.spinner.Start(""))
				}
				return s, nil
			case "enter":
				// Drill down or select
				objs := s.objects
				if idx := s.objectTable.Cursor(); idx >= 0 && idx < len(objs) {
					obj := objs[idx]
					if obj.Type == "Folder" {
						s.currentPrefix = obj.Name
						return s, tea.Batch(s.fetchObjectsCmd(), s.spinner.Start(""))
					} else {
						s.selectedObject = &obj
						s.viewState = ViewObjectDetail
					}
				}
			case "d": // Delete object (Confirm) — data-plane rm
				objs := s.objects
				if idx := s.objectTable.Cursor(); idx >= 0 && idx < len(objs) {
					obj := objs[idx]
					if obj.Type != "Folder" {
						s.selectedObject = &obj
						s.pendingAction = "delete-object"
						s.actionSource = ViewObjects
						s.viewState = ViewConfirmation
					}
				}
				return s, nil
			case "c": // Download object to local disk — data-plane cp
				objs := s.objects
				if idx := s.objectTable.Cursor(); idx >= 0 && idx < len(objs) && s.selectedBucket != nil {
					obj := objs[idx]
					if obj.Type != "Folder" {
						return s, tea.Batch(s.downloadObjectCmd(*s.selectedBucket, obj), s.spinner.Start(""))
					}
				}
				return s, nil
			case "m": // Move/rename object — data-plane mv (copy + delete source)
				objs := s.objects
				if idx := s.objectTable.Cursor(); idx >= 0 && idx < len(objs) {
					obj := objs[idx]
					if obj.Type != "Folder" {
						s.selectedObject = &obj
						s.moveForm = newMoveObjectForm(obj)
						s.viewState = ViewMoveObject
					}
				}
				return s, nil
			case "g": // Generate a signed GET URL — data-plane sign-url
				objs := s.objects
				if idx := s.objectTable.Cursor(); idx >= 0 && idx < len(objs) {
					obj := objs[idx]
					if obj.Type != "Folder" {
						s.selectedObject = &obj
						s.signURLForm = newSignURLForm(obj)
						s.viewState = ViewSignURL
					}
				}
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.objectTable.Update(msg)
			s.objectTable = updatedTable
			return s, cmd

		case ViewObjectDetail:
			switch msg.String() {
			case "q", "esc":
				s.viewState = ViewObjects
				s.selectedObject = nil
				return s, nil
			}
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View Rendering
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Buckets")
	}

	// Show animated spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetail {
		return s.renderDetailView()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewObjects {
		return s.renderObjectListView()
	}

	if s.viewState == ViewObjectDetail {
		return s.renderObjectDetailView()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}

	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}

	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}

	if s.viewState == ViewMoveObject {
		return s.moveForm.View()
	}

	if s.viewState == ViewSignURL {
		return s.signURLForm.View()
	}

	// Default: List View
	return s.renderListView()
}

func (s *Service) renderListView() string {
	// Filter Bar
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Buckets",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")

	if len(s.buckets) == 0 {
		content.WriteString(components.EmptyState("buckets"))
		return content.String()
	}

	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderObjectListView() string {
	prefix := s.currentPrefix
	if prefix == "" {
		prefix = "/"
	}
	header := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Buckets",
		s.selectedBucket.Name,
		prefix,
	)
	if len(s.objects) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, header, s.filter.View(), components.EmptyState("objects"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, header, s.filter.View(), s.objectTable.View())
}

func (s *Service) renderDetailView() string {
	if s.selectedBucket == nil {
		return "No bucket selected"
	}

	b := s.selectedBucket

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Buckets",
		b.Name,
	)

	rows := []components.KeyValue{
		{Key: "Name", Value: b.Name},
		{Key: "Location", Value: b.Location},
		{Key: "Location Type", Value: b.LocationType},
		{Key: "Class", Value: b.StorageClass},
		{Key: "Created", Value: b.Created.Format(time.RFC822)},
		{Key: "Updated", Value: b.Updated.Format(time.RFC822)},
		{Key: "Versioning", Value: formatBool(b.VersioningEnabled)},
		{Key: "Requester Pays", Value: formatBool(b.RequesterPays)},
		{Key: "Uniform Bucket Access", Value: formatBool(b.UniformBucketLevelAccess)},
		{Key: "Public Access Prevention", Value: b.PublicAccessPrevention},
		{Key: "Autoclass", Value: formatBool(b.AutoclassEnabled)},
		{Key: "Labels", Value: formatLabels(b.Labels)},
		{Key: "Lifecycle Rules", Value: fmt.Sprintf("%d", b.LifecycleRuleCount)},
		{Key: "CORS Rules", Value: fmt.Sprintf("%d", b.CORSRuleCount)},
	}
	if b.RetentionPeriod > 0 {
		rows = append(rows, components.KeyValue{Key: "Retention Period", Value: b.RetentionPeriod.String()})
	}
	if b.DefaultKMSKeyName != "" {
		rows = append(rows, components.KeyValue{Key: "Default KMS Key", Value: b.DefaultKMSKeyName})
	}
	if b.LoggingBucket != "" {
		rows = append(rows, components.KeyValue{Key: "Logging Bucket", Value: b.LoggingBucket})
	}

	view := components.DetailCard(components.DetailCardOpts{
		Title: "Bucket Details",
		Rows:  rows,
	})

	// Action Bar
	actions := components.RenderFooterHint("Enter Browse Objects | q Back")

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		"\n",
		view,
		"\n",
		actions,
	)
}

func (s *Service) renderObjectDetailView() string {
	if s.selectedObject == nil {
		return "No object selected"
	}

	o := s.selectedObject

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Buckets",
		s.selectedBucket.Name,
		o.Name,
	)

	rows := []components.KeyValue{
		{Key: "Name", Value: o.Name},
		{Key: "Content Type", Value: o.Type},
		{Key: "Size", Value: formatObjectSize(o.Size)},
		{Key: "Class", Value: o.StorageClass},
		{Key: "Created", Value: o.Created.Format(time.RFC822)},
		{Key: "Updated", Value: o.Updated.Format(time.RFC822)},
		{Key: "Generation", Value: fmt.Sprintf("%d", o.Generation)},
		{Key: "Metageneration", Value: fmt.Sprintf("%d", o.Metageneration)},
		{Key: "Event-Based Hold", Value: formatBool(o.EventBasedHold)},
		{Key: "Temporary Hold", Value: formatBool(o.TemporaryHold)},
	}
	if o.CacheControl != "" {
		rows = append(rows, components.KeyValue{Key: "Cache Control", Value: o.CacheControl})
	}
	if o.ContentEncoding != "" {
		rows = append(rows, components.KeyValue{Key: "Content Encoding", Value: o.ContentEncoding})
	}
	if o.MD5 != "" {
		rows = append(rows, components.KeyValue{Key: "MD5", Value: o.MD5})
	}
	if o.CRC32C != 0 {
		rows = append(rows, components.KeyValue{Key: "CRC32C", Value: fmt.Sprintf("%d", o.CRC32C)})
	}
	if o.KMSKeyName != "" {
		rows = append(rows, components.KeyValue{Key: "KMS Key", Value: o.KMSKeyName})
	}
	if len(o.Metadata) > 0 {
		rows = append(rows, components.KeyValue{Key: "Metadata", Value: formatLabels(o.Metadata)})
	}

	view := components.DetailCard(components.DetailCardOpts{
		Title: "Object Details",
		Rows:  rows,
	})

	actions := components.RenderFooterHint("q Back")

	return lipgloss.JoinVertical(lipgloss.Left,
		title,
		"\n",
		view,
		"\n",
		actions,
	)
}

// renderConfirmation renders the bucket-delete, object-delete, or
// IAM-grant confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "delete-object" {
		if s.selectedObject == nil {
			return "Error: No object selected"
		}
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedObject.Name,
			"object",
			fmt.Sprintf("Are you sure you want to DELETE object %s? This cannot be undone.", s.selectedObject.Name),
		)
	}
	if s.selectedBucket == nil {
		return "Error: No bucket selected"
	}
	if s.pendingAction == "grant" {
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedBucket.Name,
			"bucket",
			components.IAMConfirmMessage("bucket", s.selectedBucket.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedBucket.Name,
		"bucket",
		fmt.Sprintf("Are you sure you want to DELETE bucket %s? Only empty buckets can be deleted — this app does not support deleting objects.", s.selectedBucket.Name),
	)
}

// renderIAMView renders the current IAM policy bindings for the selected
// bucket, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedBucket == nil {
		return "Error: No bucket selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Buckets",
		s.selectedBucket.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedBucket.Name, rows)
}

// formatBool renders a boolean as Yes/No for detail views.
func formatBool(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

// formatLabels renders a string map as a compact, single-line key=value list.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}

// formatObjectSize renders a byte count as a human-readable string.
func formatObjectSize(n int64) string {
	switch {
	case n > 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/1024/1024)
	case n > 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// -----------------------------------------------------------------------------
// Helper Commands
// -----------------------------------------------------------------------------

// createBucketCmd fires the CreateBucket API call using the current form values.
func (s *Service) createBucketCmd() tea.Cmd {
	name := s.createForm.Value("Name")
	location := s.createForm.Value("Location")
	if location == "" {
		location = "US"
	}
	storageClass := s.createForm.Value("Storage Class")
	if storageClass == "" {
		storageClass = "STANDARD"
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateBucket(s.projectID, name, location, storageClass); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Bucket %s created", name)}
	}
}

// updateBucketCmd fires the UpdateBucketStorageClass API call using the
// current update-form value.
func (s *Service) updateBucketCmd(b Bucket) tea.Cmd {
	storageClass := s.updateForm.Value("Storage Class")
	if storageClass == "" {
		storageClass = b.StorageClass
	}
	versioning, _ := strconv.ParseBool(s.updateForm.Value("Versioning Enabled"))
	retentionDays, _ := strconv.ParseInt(s.updateForm.Value("Retention Days"), 10, 64)
	lifecycleDeleteAgeDays, _ := strconv.ParseInt(s.updateForm.Value("Lifecycle Delete After Days"), 10, 64)
	var corsOrigins, corsMethods []string
	if v := s.updateForm.Value("CORS Origins"); v != "" {
		corsOrigins = strings.Split(v, ",")
	}
	if v := s.updateForm.Value("CORS Methods"); v != "" {
		corsMethods = strings.Split(v, ",")
	}
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateBucketStorageClass(b.Name, storageClass); err != nil {
			return actionResultMsg{err: err}
		}
		if err := s.client.UpdateBucketSettings(b.Name, versioning, retentionDays, corsOrigins, corsMethods, lifecycleDeleteAgeDays); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating bucket %s...", b.Name)}
	}
}

// DeleteBucketCmd triggers deletion of the given bucket, only if it is
// empty — the GCS API refuses to delete a non-empty bucket, and this app
// has no object-level delete (data-plane) to empty it first, so the check
// happens client-side to give a clear error instead of a raw API failure.
func (s *Service) DeleteBucketCmd(b Bucket) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		empty, err := s.client.IsBucketEmpty(b.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		if !empty {
			return actionResultMsg{err: fmt.Errorf("bucket %s is not empty; delete its objects first (not supported in this app)", b.Name)}
		}
		if err := s.client.DeleteBucket(b.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting bucket %s...", b.Name)}
	}
}

// deleteObjectCmd deletes a single object from a bucket (data-plane rm).
func (s *Service) deleteObjectCmd(b Bucket, o Object) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteObject(b.Name, o.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleted object %s", o.Name)}
	}
}

// downloadObjectCmd downloads a single object's contents to local disk
// (data-plane cp). Non-destructive, so it runs without a confirmation step.
// moveObjectCmd renames o within b to newName via copy-then-delete-source.
func (s *Service) moveObjectCmd(b Bucket, o Object, newName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.MoveObject(b.Name, o.Name, b.Name, newName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Moved %s to %s", o.Name, newName)}
	}
}

// signURLCmd generates a signed GET URL for o, valid for expiry.
func (s *Service) signURLCmd(b Bucket, o Object, serviceAccountEmail string, expiry time.Duration) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		url, err := s.client.SignURL(b.Name, o.Name, serviceAccountEmail, expiry)
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Signed URL for %s: %s", o.Name, url)}
	}
}

func (s *Service) downloadObjectCmd(b Bucket, o Object) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return downloadResultMsg{err: fmt.Errorf("client not initialized")}
		}
		path, err := s.client.DownloadObject(b.Name, o.Name)
		if err != nil {
			return downloadResultMsg{err: err}
		}
		return downloadResultMsg{path: path}
	}
}

// removeObjectByName returns objs with the first entry named name removed,
// used to update local state after a successful object delete without a
// full refetch.
func removeObjectByName(objs []Object, name string) []Object {
	out := make([]Object, 0, len(objs))
	for _, o := range objs {
		if o.Name == name {
			continue
		}
		out = append(out, o)
	}
	return out
}

// fetchIAMCmd fetches the current IAM policy for a bucket.
func (s *Service) fetchIAMCmd(b Bucket) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetBucketIAMPolicy(b.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given bucket.
func (s *Service) addIAMBindingCmd(b Bucket, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddBucketIAMBinding(b.Name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on bucket %s", role, member, b.Name)}
	}
}

func (s *Service) fetchBucketsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "gcs_buckets"

		// 1. Check Cache
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if buckets, ok := val.([]Bucket); ok {
					return bucketsMsg(buckets)
				}
			}
		}

		// 2. API Call
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		buckets, err := s.client.ListBuckets(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		// 3. Update Cache
		if s.cache != nil {
			s.cache.Set(key, buckets, CacheTTL)
		}

		return bucketsMsg(buckets)
	}
}

func (s *Service) updateTable(items []Bucket) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.StorageClass,
			item.Created.Format("2006-01-02"),
		}
	}
	s.table.SetRows(rows)
}

// getFilteredBuckets returns filtered buckets based on the query string
func (s *Service) getFilteredBuckets(buckets []Bucket, query string) []Bucket {
	if query == "" {
		return buckets
	}
	return components.FilterSlice(buckets, query, func(bucket Bucket, q string) bool {
		return components.ContainsMatch(bucket.Name, bucket.Location, bucket.StorageClass)(q)
	})
}

func (s *Service) getFilteredObjects(objects []Object, query string) []Object {
	if query == "" {
		return objects
	}
	return components.FilterSlice(objects, query, func(obj Object, q string) bool {
		return components.ContainsMatch(obj.Name, obj.Type)(q)
	})
}

func (s *Service) fetchObjectsCmd() tea.Cmd {
	return func() tea.Msg {
		// No caching for object browsing for now to keep it simple and fresh
		if s.selectedBucket == nil {
			return errMsg(fmt.Errorf("no bucket selected"))
		}

		objs, err := s.client.ListObjects(s.selectedBucket.Name, s.currentPrefix)
		if err != nil {
			return errMsg(err)
		}
		return objectsMsg(objs)
	}
}

func (s *Service) updateObjectTable(items []Object) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		sizeStr := fmt.Sprintf("%d B", item.Size)
		if item.Type == "Folder" {
			sizeStr = "-"
		} else if item.Size > 1024*1024 {
			sizeStr = fmt.Sprintf("%.1f MB", float64(item.Size)/1024/1024)
		} else if item.Size > 1024 {
			sizeStr = fmt.Sprintf("%.1f KB", float64(item.Size)/1024)
		}

		rows[i] = table.Row{
			item.Name,
			item.Type,
			sizeStr,
			item.Updated.Format("Jan 02 15:04"),
		}
	}
	s.objectTable.SetRows(rows)
}

// parentPrefix calculates the parent folder prefix
func parentPrefix(p string) string {
	if p == "" {
		return ""
	}
	// Remove trailing slash if exists (folders usually have it)
	if p[len(p)-1] == '/' {
		p = p[:len(p)-1]
	}
	// Find last slash
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			return p[:i+1]
		}
	}
	return ""
}
