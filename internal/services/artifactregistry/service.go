package artifactregistry

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 30 * time.Second

// =============================================================================
// Models
// =============================================================================

// RepositoryItem represents an Artifact Registry repository
// IAMBinding is a single role -> members pair from a repository's IAM policy.
type IAMBinding struct {
	Role    string
	Members []string
}

type RepositoryItem struct {
	Name                string // repository ID (last path segment)
	Format              string // DOCKER, MAVEN, NPM, PYTHON, APT, YUM, GO, KFP, GENERIC, ...
	Mode                string // STANDARD_REPOSITORY, VIRTUAL_REPOSITORY, REMOTE_REPOSITORY
	Region              string // location, e.g. us-central1
	SizeBytes           int64
	Description         string
	CreateTime          time.Time
	UpdateTime          time.Time
	Labels              map[string]string
	KmsKeyName          string
	RegistryUri         string
	CleanupPolicyCount  int
	CleanupPolicyDryRun bool
}

// DockerImage represents a single image (version) stored in an Artifact
// Registry Docker repository.
type DockerImage struct {
	Name           string // full version resource name, used for deletion
	URI            string
	Tags           []string
	ImageSizeBytes int64
	UploadTime     time.Time
	BuildTime      time.Time
	MediaType      string
}

// RepositoryCreateOpts holds the minimal set of fields needed to create an
// Artifact Registry repository via the Create form.
type RepositoryCreateOpts struct {
	RepositoryID string
	Location     string
	Format       string // DOCKER, MAVEN, NPM, PYTHON, APT, YUM, GO, KFP, GENERIC, ...
	Description  string
}

// Tick message for background refresh
type tickMsg time.Time

// ViewState defines the current UI state of the service
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewCreate
	ViewUpdate
	ViewImages
	ViewConfirmation
	ViewIAM
	ViewIAMForm
)

// newRepoUpdateForm builds the FormModel for updating a repository's
// description, seeded with its current value. set-cleanup-policies and
// other repository fields are out of scope for this minimal Update flow.
func newRepoUpdateForm(item RepositoryItem) components.FormModel {
	return components.NewForm("Update Repository: "+item.Name, []components.FormField{
		{Label: "Description", Default: item.Description},
	})
}

// Message types for async operations
type dataMsg []RepositoryItem
type imagesMsg []DockerImage
type errMsg error

// actionResultMsg carries the result of an async mutating action (e.g.
// repository creation).
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetRepositoryIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// =============================================================================
// Service Definition
// =============================================================================

// Service implements the services.Service interface
type Service struct {
	client    *Client
	projectID string

	// Dimensions (for custom layouts)
	width  int
	height int

	// UI Components
	table              *components.StandardTable
	filter             components.FilterModel
	filterSession      components.FilterSession[RepositoryItem]
	imageTable         *components.StandardTable
	imageFilter        components.FilterModel
	imageFilterSession components.FilterSession[DockerImage]
	spinner            components.SpinnerModel

	// Data State
	items  []RepositoryItem
	images []DockerImage
	err    error
	loaded bool // Track if initial data has been loaded

	// View State
	viewState     ViewState
	selectedItem  *RepositoryItem
	selectedImage *DockerImage

	createForm components.FormModel
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// IAM: current bindings for the selected repository, and the
	// add-binding form. pendingIAMRole/pendingIAMMember are captured at
	// form-submit time so the confirmation dialog and the actual API call
	// use the same values regardless of what the form fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Cache
	cache *core.Cache
}

// NewService creates a new instance of the service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Format", Width: 12},
		{Title: "Mode", Width: 20},
		{Title: "Region", Width: 15},
	}

	t := components.NewStandardTable(columns)

	imageColumns := []table.Column{
		{Title: "Tags", Width: 30},
		{Title: "Size", Width: 12},
		{Title: "Uploaded", Width: 20},
		{Title: "Media Type", Width: 30},
	}
	imgTable := components.NewStandardTable(imageColumns)

	svc := &Service{
		table:       t,
		filter:      components.NewFilterWithPlaceholder("Filter items..."),
		imageTable:  imgTable,
		imageFilter: components.NewFilterWithPlaceholder("Filter images..."),
		spinner:     components.NewSpinner(),
		viewState:   ViewList,
		cache:       cache,
		loaded:      false,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredItems, svc.updateTable)
	svc.imageFilterSession = components.NewFilterSession(&svc.imageFilter, svc.getFilteredImages, svc.updateImageTable)
	return svc
}

// Name returns the full human-readable name
func (s *Service) Name() string {
	return "Artifact Registry"
}

// ShortName returns the identifier used for routing (e.g., "gce", "sql")
func (s *Service) ShortName() string {
	return "artifactregistry"
}

// HelpText returns context-aware keybindings for the status bar
func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewList:
		return "r:Refresh  /:Filter  Enter:Detail  n:New Repository"
	case ViewDetail:
		return "Esc/q:Back  u:Update  d:Delete  m:Images  i:IAM"
	case ViewImages:
		return "Esc/q:Back  /:Filter  d:Delete"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewIAM:
		return "a:Add Binding  q/Esc:Back"
	case ViewIAMForm:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	case ViewCreate, ViewUpdate:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	default:
		return ""
	}
}

// =============================================================================
// Lifecycle & Interface Implementation
// =============================================================================

// InitService initializes the API client - called once when service is first accessed
func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx)
	if err != nil {
		return err
	}
	s.client = client
	return nil
}

// Reinit reinitializes the service with a new project ID (on project switch)
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	s.loaded = false // Force reload on next entry
	return s.InitService(ctx, projectID)
}

// Init returns startup commands (background tick)
func (s *Service) Init() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchDataCmd(false), s.tick())
}

// tick creates a background ticker for cache invalidation/refresh
func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Refresh triggers a forced data reload with spinner
func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""), // Empty string = playful random messages
		s.fetchDataCmd(true),
	)
}

// Reset clears the service state when navigating away
func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedItem = nil
	s.selectedImage = nil
	s.images = nil
	s.err = nil // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0)
	s.filter.ExitFilterMode()
	s.imageTable.SetCursor(0)
	s.imageFilter.ExitFilterMode()
}

// IsRootView returns true if at the top-level list (used for 'q' navigation)
func (s *Service) IsRootView() bool {
	return s.viewState == ViewList
}

// Focus handles input focus - triggers initial load if needed
func (s *Service) Focus() {
	s.table.Focus()
	s.imageTable.Focus()
}

// Blur handles loss of input focus
func (s *Service) Blur() {
	s.table.Blur()
	s.imageTable.Blur()
}

// =============================================================================
// Update Loop
// =============================================================================

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchDataCmd(false), s.tick())

	case dataMsg:
		s.spinner.Stop()
		s.items = msg
		s.loaded = true
		s.filterSession.Apply(s.items)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case imagesMsg:
		s.spinner.Stop()
		s.images = msg
		s.imageFilterSession.Apply(s.images)
		return s, nil

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

	case actionResultMsg:
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if s.selectedItem != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedItem),
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
			if s.actionSource == ViewImages && s.selectedItem != nil {
				s.selectedImage = nil
				return s, tea.Batch(
					func() tea.Msg {
						return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
					},
					s.fetchImagesCmd(*s.selectedItem, true),
				)
			}
			s.selectedItem = nil
			s.viewState = ViewList
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
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
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		s.imageTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		if s.viewState == ViewList {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}
		if s.viewState == ViewImages {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.imageTable.Update(msg)
			s.imageTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

// handleKeyMsg processes keyboard input based on current view state
func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	if s.viewState == ViewCreate {
		result, formCmd := s.createForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewList
			return s, nil
		}
		if result.Submitted {
			return s, s.createRepositoryCmd()
		}
		return s, formCmd
	}

	if s.viewState == ViewUpdate {
		result, formCmd := s.updateForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewDetail
			return s, nil
		}
		if result.Submitted && s.selectedItem != nil {
			return s, s.updateRepositoryCmd(*s.selectedItem)
		}
		return s, formCmd
	}

	if s.viewState == ViewIAMForm {
		result, formCmd := s.iamForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewIAM
			return s, nil
		}
		if result.Submitted && s.selectedItem != nil {
			s.pendingIAMRole = s.iamForm.Value("Role")
			s.pendingIAMMember = s.iamForm.Value("Member")
			s.pendingAction = "grant"
			s.actionSource = ViewIAM
			s.viewState = ViewConfirmation
			return s, nil
		}
		return s, formCmd
	}

	if s.viewState == ViewIAM {
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewDetail
			return s, nil
		case "a":
			if s.selectedItem != nil {
				s.iamForm = components.NewIAMAddBindingForm(s.selectedItem.Name)
				s.viewState = ViewIAMForm
			}
			return s, nil
		}
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

		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "n":
			s.createForm = components.NewForm("New Repository", []components.FormField{
				{Label: "Repository ID", Required: true},
				{Label: "Location", Required: true},
				{Label: "Format", Default: "DOCKER", Required: true},
				{Label: "Description"},
			})
			s.viewState = ViewCreate
			return s, nil
		case "enter":
			items := s.getCurrentItems()
			if idx := s.table.Cursor(); idx >= 0 && idx < len(items) {
				s.selectedItem = &items[idx]
				s.viewState = ViewDetail
			}
			return s, nil
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
			s.selectedItem = nil
			return s, nil
		case "u":
			if s.selectedItem != nil {
				s.updateForm = newRepoUpdateForm(*s.selectedItem)
				s.viewState = ViewUpdate
			}
			return s, nil
		case "d":
			if s.selectedItem != nil {
				s.pendingAction = "delete"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "m":
			if s.selectedItem != nil {
				s.viewState = ViewImages
				return s, tea.Batch(s.spinner.Start(""), s.fetchImagesCmd(*s.selectedItem, false))
			}
		case "i":
			if s.selectedItem != nil {
				return s, tea.Batch(s.fetchIAMCmd(*s.selectedItem), s.spinner.Start(""))
			}
		}
	}

	if s.viewState == ViewImages {
		result := s.imageFilterSession.HandleKey(msg)
		if result.Handled {
			if result.Cmd != nil {
				return s, result.Cmd
			}
			if !result.ShouldContinue {
				return s, nil
			}
		}

		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewDetail
			s.selectedImage = nil
			return s, nil
		case "d":
			images := s.getFilteredImages(s.images, s.imageFilter.Value())
			if idx := s.imageTable.Cursor(); idx >= 0 && idx < len(images) {
				s.selectedImage = &images[idx]
				s.pendingAction = "delete"
				s.actionSource = ViewImages
				s.viewState = ViewConfirmation
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.imageTable.Update(msg)
		s.imageTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewConfirmation {
		switch msg.String() {
		case "y", "enter":
			var actionCmd tea.Cmd
			if s.pendingAction == "delete" {
				if s.actionSource == ViewImages && s.selectedImage != nil {
					actionCmd = s.deleteImageCmd(*s.selectedImage)
				} else if s.selectedItem != nil {
					actionCmd = s.deleteRepositoryCmd(*s.selectedItem)
				}
			} else if s.pendingAction == "grant" && s.selectedItem != nil {
				actionCmd = s.addIAMBindingCmd(*s.selectedItem, s.pendingIAMRole, s.pendingIAMMember)
			}
			s.viewState = s.actionSource
			return s, actionCmd

		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction = ""
			return s, nil
		}
	}

	return s, nil
}

// =============================================================================
// View Rendering
// =============================================================================

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Repositories")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewDetail:
		return s.renderDetailView()
	case ViewImages:
		return s.renderImagesView()
	case ViewConfirmation:
		return s.renderConfirmation()
	case ViewIAM:
		return s.renderIAMView()
	case ViewIAMForm:
		return s.iamForm.View()
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

// renderConfirmation renders the delete confirmation dialog — a repository
// delete (destructive: wipes every image/package/version inside it) or an
// image delete, depending on which drill-down level triggered it.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "grant" {
		if s.selectedItem == nil {
			return "Error: No repository selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedItem.Name,
			"repository",
			components.IAMConfirmMessage("repository", s.selectedItem.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	if s.actionSource == ViewImages {
		if s.selectedImage == nil {
			return "Error: No image selected"
		}
		name := s.selectedImage.URI
		if len(s.selectedImage.Tags) > 0 {
			name = strings.Join(s.selectedImage.Tags, ", ")
		}
		return components.RenderConfirmation(s.pendingAction, name, "image")
	}
	if s.selectedItem == nil {
		return "Error: No repository selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedItem.Name,
		"repository",
		fmt.Sprintf("Are you sure you want to DELETE repository %s? This deletes everything in it (all images/packages/versions).", s.selectedItem.Name),
	)
}

// createRepositoryCmd fires the CreateRepository API call using the current
// createForm values.
func (s *Service) createRepositoryCmd() tea.Cmd {
	v := s.createForm.Values()
	opts := RepositoryCreateOpts{
		RepositoryID: v["Repository ID"],
		Location:     v["Location"],
		Format:       v["Format"],
		Description:  v["Description"],
	}
	s.viewState = ViewList
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateRepository(s.projectID, opts); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Creating repository %s...", opts.RepositoryID)}
	}
}

// updateRepositoryCmd fires the UpdateRepositoryDescription API call using
// the current update-form value.
func (s *Service) updateRepositoryCmd(item RepositoryItem) tea.Cmd {
	description := s.updateForm.Value("Description")
	s.viewState = ViewDetail
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateRepositoryDescription(s.projectID, item.Region, item.Name, description); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating repository %s...", item.Name)}
	}
}

// deleteRepositoryCmd triggers deletion of the given repository
func (s *Service) deleteRepositoryCmd(item RepositoryItem) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteRepository(s.projectID, item.Region, item.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting repository %s...", item.Name)}
	}
}

// repoFullName builds the full repository resource name, matching the
// pattern already used for image listing (see fetchImagesCmd).
func (s *Service) repoFullName(item RepositoryItem) string {
	return fmt.Sprintf("projects/%s/locations/%s/repositories/%s", s.projectID, item.Region, item.Name)
}

// renderIAMView renders the current IAM policy bindings for the selected
// repository, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedItem == nil {
		return "Error: No repository selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Repositories",
		s.selectedItem.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedItem.Name, rows)
}

// fetchIAMCmd fetches the current IAM policy for a repository.
func (s *Service) fetchIAMCmd(item RepositoryItem) tea.Cmd {
	name := s.repoFullName(item)
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetRepositoryIAMPolicy(name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given repository.
func (s *Service) addIAMBindingCmd(item RepositoryItem, role, member string) tea.Cmd {
	name := s.repoFullName(item)
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddRepositoryIAMBinding(name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on repository %s", role, member, item.Name)}
	}
}

func (s *Service) renderImagesView() string {
	if s.selectedItem == nil {
		return "Error: No repository selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedItem.Name,
		"Images",
	)

	content := s.imageTable.View()
	if len(s.images) == 0 {
		content = components.EmptyState("images")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.imageFilter.View(),
		content,
	)
}

func (s *Service) renderListView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
	)

	content := s.table.View()
	if len(s.items) == 0 {
		content = components.EmptyState("repositories")
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.filter.View(),
		content,
	)
}

func (s *Service) renderDetailView() string {
	if s.selectedItem == nil {
		return "Error: No item selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedItem.Name,
	)

	sizeStr := formatBytes(s.selectedItem.SizeBytes)

	rows := []components.KeyValue{
		{Key: "Name", Value: s.selectedItem.Name},
		{Key: "Format", Value: s.selectedItem.Format},
		{Key: "Mode", Value: s.selectedItem.Mode},
		{Key: "Region", Value: s.selectedItem.Region},
		{Key: "Size", Value: sizeStr},
	}
	if s.selectedItem.Description != "" {
		rows = append(rows, components.KeyValue{Key: "Description", Value: s.selectedItem.Description})
	}
	if s.selectedItem.RegistryUri != "" {
		rows = append(rows, components.KeyValue{Key: "Registry URI", Value: s.selectedItem.RegistryUri})
	}
	if s.selectedItem.KmsKeyName != "" {
		rows = append(rows, components.KeyValue{Key: "KMS Key", Value: s.selectedItem.KmsKeyName})
	}
	if len(s.selectedItem.Labels) > 0 {
		rows = append(rows, components.KeyValue{Key: "Labels", Value: formatLabels(s.selectedItem.Labels)})
	}
	if s.selectedItem.CleanupPolicyCount > 0 {
		cleanup := fmt.Sprintf("%d policies", s.selectedItem.CleanupPolicyCount)
		if s.selectedItem.CleanupPolicyDryRun {
			cleanup += " (dry run)"
		}
		rows = append(rows, components.KeyValue{Key: "Cleanup Policies", Value: cleanup})
	}
	if !s.selectedItem.CreateTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Created", Value: s.selectedItem.CreateTime.Format("2006-01-02 15:04:05")})
	}
	if !s.selectedItem.UpdateTime.IsZero() {
		rows = append(rows, components.KeyValue{Key: "Updated", Value: s.selectedItem.UpdateTime.Format("2006-01-02 15:04:05")})
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Repository Details",
		Rows:  rows,
	})

	actions := components.RenderFooterHint("q Back")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		card,
		"",
		actions,
	)
}

// =============================================================================
// Data Fetching
// =============================================================================

func (s *Service) fetchDataCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("artifactregistry_items:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if items, ok := val.([]RepositoryItem); ok {
					return dataMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		items, err := s.client.ListRepositories(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, items, CacheTTL)
		}

		return dataMsg(items)
	}
}

// fetchImagesCmd fetches the Docker images for the given repository.
func (s *Service) fetchImagesCmd(repo RepositoryItem, force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("artifactregistry_images:%s:%s:%s", s.projectID, repo.Region, repo.Name)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if images, ok := val.([]DockerImage); ok {
					return imagesMsg(images)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		parent := fmt.Sprintf("projects/%s/locations/%s/repositories/%s", s.projectID, repo.Region, repo.Name)
		images, err := s.client.ListDockerImages(parent)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, images, CacheTTL)
		}

		return imagesMsg(images)
	}
}

func (s *Service) deleteImageCmd(img DockerImage) tea.Cmd {
	return func() tea.Msg {
		err := s.client.DeleteImage(img.Name)
		if err != nil {
			return actionResultMsg{err: err}
		}
		name := img.URI
		if len(img.Tags) > 0 {
			name = strings.Join(img.Tags, ", ")
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting image %s...", name)}
	}
}

// =============================================================================
// Table Updates
// =============================================================================

func (s *Service) updateTable(items []RepositoryItem) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Format,
			item.Mode,
			item.Region,
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) getCurrentItems() []RepositoryItem {
	return s.getFilteredItems(s.items, s.filter.Value())
}

func (s *Service) getFilteredItems(items []RepositoryItem, query string) []RepositoryItem {
	if query == "" {
		return items
	}
	return components.FilterSlice(items, query, func(item RepositoryItem, q string) bool {
		return components.ContainsMatch(item.Name, item.Format, item.Mode, item.Region)(q)
	})
}

func (s *Service) updateImageTable(images []DockerImage) {
	rows := make([]table.Row, len(images))
	for i, img := range images {
		uploaded := ""
		if !img.UploadTime.IsZero() {
			uploaded = img.UploadTime.Format("2006-01-02 15:04:05")
		}
		tags := strings.Join(img.Tags, ", ")
		if tags == "" {
			tags = "<untagged>"
		}
		rows[i] = table.Row{
			tags,
			formatBytes(img.ImageSizeBytes),
			uploaded,
			img.MediaType,
		}
	}
	s.imageTable.SetRows(rows)
}

func (s *Service) getFilteredImages(images []DockerImage, query string) []DockerImage {
	if query == "" {
		return images
	}
	return components.FilterSlice(images, query, func(img DockerImage, q string) bool {
		return components.ContainsMatch(append([]string{img.URI, img.MediaType}, img.Tags...)...)(q)
	})
}

// formatLabels renders a label map as a compact, single-line key=value list
// for display in a detail card row.
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

// formatBytes renders a byte count as a human-readable string (e.g. "1.2 MB").
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}
