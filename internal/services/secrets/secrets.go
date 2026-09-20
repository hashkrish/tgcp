package secrets

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 60 * time.Second // Secrets rarely change

// -----------------------------------------------------------------------------
// Message Types
// -----------------------------------------------------------------------------

type tickMsg time.Time
type secretsMsg []Secret
type versionsMsg []SecretVersion
type errMsg error
type revealedMsg string

// revealErrMsg is a distinct concrete type (not `type revealErrMsg error`)
// so it doesn't collide with errMsg in the tea.Msg type switch below —
// two named interface types with the same method set (both just wrapping
// `error`) match identically in a type switch, silently making whichever
// case is listed second unreachable (caught by staticcheck's SA4020).
type revealErrMsg struct{ err error }

// ViewState defines the current UI state
type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewVersions
	ViewVersionDetail
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewIAM
	ViewIAMForm
	ViewAddVersion
)

// newSecretUpdateForm builds the FormModel for updating a secret's labels,
// seeded with its current labels rendered as comma-separated key=value
// pairs. Replication policy changes are out of scope for this minimal
// Update flow.
func newSecretUpdateForm(sec Secret) components.FormModel {
	var pairs []string
	for k, v := range sec.Labels {
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
	}
	return components.NewForm("Update Secret: "+sec.Name, []components.FormField{
		{Label: "Labels (key=value,key2=value2)", Default: strings.Join(pairs, ",")},
	})
}

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetSecretIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

// Service implements the services.Service interface for Secret Manager
type Service struct {
	client    *Client
	projectID string

	// Dimensions
	width  int
	height int

	// UI Components
	table             *components.StandardTable
	versionTable      *components.StandardTable
	detailList        components.DetailList
	versionDetailList components.DetailList
	filter            components.FilterModel
	filterSession     components.FilterSession[Secret]
	spinner           components.SpinnerModel

	// Data State
	secrets  []Secret
	versions []SecretVersion
	err      error

	// View State
	viewState       ViewState
	selectedSecret  *Secret
	selectedVersion *SecretVersion

	// Reveal state — session-only, never persisted or cached. Only the
	// "is currently revealed" boolean and the fetched value live here, and
	// both are cleared whenever the user navigates away from the version
	// detail view (see clearReveal).
	revealed      bool
	revealedValue string
	revealErr     error

	createForm     components.FormModel
	updateForm     components.FormModel
	addVersionForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// pendingVersionValue is the plaintext value captured at add-version-form
	// submit time, so the confirmation dialog and the actual AddVersion call
	// use the same value regardless of what the form field holds later.
	pendingVersionValue string

	// IAM: current bindings for the selected secret, and the add-binding
	// form. pendingIAMRole/pendingIAMMember are captured at form-submit
	// time so the confirmation dialog and the actual API call use the same
	// values regardless of what the form fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string

	// Cache
	cache *core.Cache
}

// NewService creates a new Secret Manager service
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Replication", Width: 15},
		{Title: "Labels", Width: 25},
		{Title: "Created", Width: 20},
	}

	t := components.NewStandardTable(columns)

	versionColumns := []table.Column{
		{Title: "Version", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Created", Width: 25},
	}
	vt := components.NewStandardTable(versionColumns)

	svc := &Service{
		table:        t,
		versionTable: vt,
		filter:       components.NewFilterWithPlaceholder("Filter secrets..."),
		spinner:      components.NewSpinner(),
		viewState:    ViewList,
		cache:        cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredSecrets, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "Secret Manager"
}

func (s *Service) ShortName() string {
	return "secrets"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewList:
		return "r:Refresh  /:Filter  n:New Secret  Enter:Detail"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewDetail:
		return "↑↓:Select  y:Copy  v:Versions  u:Update  n:New Version  d:Delete  i:IAM  Esc/q:Back"
	case ViewIAM:
		return "a:Add Binding  q/Esc:Back"
	case ViewIAMForm:
		return "Tab/↑↓:Move  Enter/Ctrl+S:Submit  Esc:Cancel"
	case ViewVersions:
		return "Enter:View Version  Esc/q:Back to Detail"
	case ViewVersionDetail:
		reveal := "v:Reveal Value"
		copyValue := ""
		if s.revealed {
			reveal = "v:Hide Value"
			copyValue = "  Y:Copy Value"
		}
		return "↑↓:Select  y:Copy Field  " + reveal + copyValue + "  E:Enable  D:Disable  X:Destroy  Esc/q:Back"
	case ViewCreate, ViewUpdate, ViewAddVersion:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
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

func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchSecretsCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchSecretsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedSecret = nil
	s.versions = nil
	s.err = nil
	s.table.SetCursor(0)
	s.versionTable.SetCursor(0)
	s.filter.ExitFilterMode()
	s.clearReveal()
}

// clearReveal wipes any revealed secret value and the revealed flag. It is
// called any time the user leaves the version detail view, so a revealed
// value never survives navigation and is never cached anywhere.
func (s *Service) clearReveal() {
	s.selectedVersion = nil
	s.revealed = false
	s.revealedValue = ""
	s.revealErr = nil
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
		return s, tea.Batch(s.fetchSecretsCmd(false), s.tick())

	case secretsMsg:
		s.spinner.Stop()
		s.secrets = msg
		s.filterSession.Apply(s.secrets)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case versionsMsg:
		s.spinner.Stop()
		s.versions = msg
		s.updateVersionTable(msg)
		if s.selectedVersion != nil {
			for i := range s.versions {
				if s.versions[i].FullName == s.selectedVersion.FullName {
					s.selectedVersion = &s.versions[i]
					break
				}
			}
		}
		return s, nil

	case revealedMsg:
		s.spinner.Stop()
		s.revealed = true
		s.revealedValue = string(msg)
		s.revealErr = nil
		return s, nil

	case revealErrMsg:
		s.spinner.Stop()
		s.revealed = false
		s.revealedValue = ""
		s.revealErr = msg.err
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
			if s.selectedSecret != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedSecret),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.pendingAction == "delete" || s.pendingAction == "version-enable" || s.pendingAction == "version-disable" || s.pendingAction == "version-destroy" || s.pendingAction == "add-version" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			if action == "delete" {
				s.selectedSecret = nil
				s.viewState = ViewList
				return s, tea.Batch(
					func() tea.Msg {
						return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
					},
					s.Refresh(),
				)
			}
			// Version-level actions: stay on the version detail view and
			// refresh the version list in the background so the state
			// column picks up the change.
			var refreshCmd tea.Cmd
			if s.selectedSecret != nil {
				refreshCmd = s.fetchVersionsCmd(s.selectedSecret.FullName)
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				refreshCmd,
			)
		}
		wasUpdate := s.viewState == ViewUpdate
		if msg.err != nil {
			if wasUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
		if wasUpdate {
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
		s.width = msg.Width
		s.height = msg.Height
		s.table.HandleWindowSizeDefault(msg)
		s.versionTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		// Forward mouse events to active table for click selection
		switch s.viewState {
		case ViewList:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		case ViewVersions:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.versionTable.Update(msg)
			s.versionTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch s.viewState {
	case ViewList:
		// Handle filter
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
			s.createForm = components.NewForm("New Secret", []components.FormField{
				{Label: "Secret ID", Placeholder: "my-secret", Required: true},
				{Label: "Replication", Default: "automatic"},
			})
			s.viewState = ViewCreate
			return s, nil
		case "enter":
			secrets := s.getCurrentSecrets()
			if idx := s.table.Cursor(); idx >= 0 && idx < len(secrets) {
				s.selectedSecret = &secrets[idx]
				s.detailList = components.NewDetailList("Secret Details", nil)
				s.viewState = ViewDetail
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.table.Update(msg)
		s.table = updatedTable
		return s, cmd

	case ViewCreate:
		result, formCmd := s.createForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewList
			return s, nil
		}
		if result.Submitted {
			return s, s.submitCreateCmd()
		}
		return s, formCmd

	case ViewUpdate:
		result, formCmd := s.updateForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewDetail
			return s, nil
		}
		if result.Submitted && s.selectedSecret != nil {
			return s, s.submitUpdateCmd(*s.selectedSecret)
		}
		return s, formCmd

	case ViewAddVersion:
		result, formCmd := s.addVersionForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewDetail
			return s, nil
		}
		if result.Submitted && s.selectedSecret != nil {
			s.pendingVersionValue = s.addVersionForm.Value("Secret Value")
			s.pendingAction = "add-version"
			s.actionSource = ViewDetail
			s.viewState = ViewConfirmation
			return s, nil
		}
		return s, formCmd

	case ViewIAMForm:
		result, formCmd := s.iamForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewIAM
			return s, nil
		}
		if result.Submitted && s.selectedSecret != nil {
			s.pendingIAMRole = s.iamForm.Value("Role")
			s.pendingIAMMember = s.iamForm.Value("Member")
			s.pendingAction = "grant"
			s.actionSource = ViewIAM
			s.viewState = ViewConfirmation
			return s, nil
		}
		return s, formCmd

	case ViewIAM:
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewDetail
			return s, nil
		case "a":
			if s.selectedSecret != nil {
				s.iamForm = components.NewIAMAddBindingForm(s.selectedSecret.Name)
				s.viewState = ViewIAMForm
			}
			return s, nil
		}

	case ViewDetail:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewList
			s.selectedSecret = nil
			s.versions = nil
			return s, nil
		case "up", "k":
			s.detailList.CursorUp()
			return s, nil
		case "down", "j":
			s.detailList.CursorDown()
			return s, nil
		case "y":
			if row, ok := s.detailList.Selected(); ok {
				return s, components.CopyToClipboardCmd(row.Key, row.Value)
			}
			return s, nil
		case "u":
			if s.selectedSecret != nil {
				s.updateForm = newSecretUpdateForm(*s.selectedSecret)
				s.viewState = ViewUpdate
			}
			return s, nil
		case "v":
			// Fetch versions for selected secret
			if s.selectedSecret != nil {
				s.viewState = ViewVersions
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchVersionsCmd(s.selectedSecret.FullName),
				)
			}
		case "n":
			if s.selectedSecret != nil {
				s.addVersionForm = components.NewForm("Add New Version: "+s.selectedSecret.Name, []components.FormField{
					{Label: "Secret Value", Placeholder: "new secret value", Required: true},
				})
				s.viewState = ViewAddVersion
			}
			return s, nil
		case "d":
			if s.selectedSecret != nil {
				s.pendingAction = "delete"
				s.actionSource = ViewDetail
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "i":
			if s.selectedSecret != nil {
				return s, tea.Batch(s.fetchIAMCmd(*s.selectedSecret), s.spinner.Start(""))
			}
			return s, nil
		}

	case ViewConfirmation:
		switch msg.String() {
		case "y", "enter":
			var actionCmd tea.Cmd
			switch s.pendingAction {
			case "delete":
				if s.selectedSecret != nil {
					actionCmd = s.deleteSecretCmd(*s.selectedSecret)
				}
			case "grant":
				if s.selectedSecret != nil {
					actionCmd = s.addIAMBindingCmd(*s.selectedSecret, s.pendingIAMRole, s.pendingIAMMember)
				}
			case "version-enable":
				if s.selectedVersion != nil {
					actionCmd = s.enableVersionCmd(*s.selectedVersion)
				}
			case "version-disable":
				if s.selectedVersion != nil {
					actionCmd = s.disableVersionCmd(*s.selectedVersion)
				}
			case "version-destroy":
				if s.selectedVersion != nil {
					actionCmd = s.destroyVersionCmd(*s.selectedVersion)
				}
			case "add-version":
				if s.selectedSecret != nil {
					actionCmd = s.addVersionCmd(*s.selectedSecret, s.pendingVersionValue)
				}
			}
			s.viewState = s.actionSource
			return s, actionCmd
		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction = ""
			return s, nil
		}

	case ViewVersions:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewDetail
			return s, nil
		case "enter":
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.clearReveal()
				s.selectedVersion = &s.versions[idx]
				s.versionDetailList = components.NewDetailList("Secret Version Details", nil)
				s.viewState = ViewVersionDetail
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.versionTable.Update(msg)
		s.versionTable = updatedTable
		return s, cmd

	case ViewVersionDetail:
		switch msg.String() {
		case "esc", "q":
			s.viewState = ViewVersions
			s.clearReveal()
			return s, nil
		case "up", "k":
			s.versionDetailList.CursorUp()
			return s, nil
		case "down", "j":
			s.versionDetailList.CursorDown()
			return s, nil
		case "y":
			if row, ok := s.versionDetailList.Selected(); ok {
				return s, components.CopyToClipboardCmd(row.Key, row.Value)
			}
			return s, nil
		case "Y": // Copy the revealed secret value itself (handles multi-line values that can't be a DetailList row)
			if s.revealed {
				return s, components.CopyToClipboardCmd("Value", s.revealedValue)
			}
			return s, nil
		case "v":
			// Explicit, deliberate reveal/hide toggle. The value is never
			// fetched automatically just from navigating here — only this
			// keypress triggers the API call.
			if s.selectedVersion == nil {
				return s, nil
			}
			if s.revealed {
				// Hide: drop the cached plaintext immediately.
				s.revealed = false
				s.revealedValue = ""
				return s, nil
			}
			return s, tea.Batch(
				s.spinner.Start(""),
				s.fetchRevealCmd(s.selectedVersion.FullName),
			)
		case "E": // Enable (Confirm)
			if s.selectedVersion != nil {
				s.pendingAction = "version-enable"
				s.actionSource = ViewVersionDetail
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "D": // Disable (Confirm)
			if s.selectedVersion != nil {
				s.pendingAction = "version-disable"
				s.actionSource = ViewVersionDetail
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "X": // Destroy (Confirm)
			if s.selectedVersion != nil {
				s.pendingAction = "version-destroy"
				s.actionSource = ViewVersionDetail
				s.viewState = ViewConfirmation
			}
			return s, nil
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// View
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Secrets")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewDetail:
		return s.renderDetailView()
	case ViewVersions:
		return s.renderVersionsView()
	case ViewVersionDetail:
		return s.renderVersionDetailView()
	case ViewCreate:
		return s.createForm.View()
	case ViewUpdate:
		return s.updateForm.View()
	case ViewAddVersion:
		return s.addVersionForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	case ViewIAM:
		return s.renderIAMView()
	case ViewIAMForm:
		return s.iamForm.View()
	default:
		return s.renderListView()
	}
}

// renderIAMView renders the current IAM policy bindings for the selected
// secret, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedSecret == nil {
		return "Error: No secret selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Secrets",
		s.selectedSecret.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedSecret.Name, rows)
}

// fetchIAMCmd fetches the current IAM policy for a secret.
func (s *Service) fetchIAMCmd(sec Secret) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetSecretIAMPolicy(sec.FullName)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given secret.
func (s *Service) addIAMBindingCmd(sec Secret, role, member string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret",
			Name: sec.Name, Action: "grant",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.AddSecretIAMBinding(sec.FullName, role, member)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on secret %s", role, member, sec.Name)}
	}
}

// renderConfirmation renders the secret-delete or version-level (enable /
// disable / destroy) confirmation dialog.
func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "delete":
		if s.selectedSecret == nil {
			return "Error: No secret selected"
		}
		return components.RenderConfirmationWithMessage(
			s.pendingAction,
			s.selectedSecret.Name,
			"secret",
			fmt.Sprintf("Are you sure you want to DELETE secret %s? This deletes every version of it.", s.selectedSecret.Name),
		)
	case "grant":
		if s.selectedSecret == nil {
			return "Error: No secret selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedSecret.Name,
			"secret",
			components.IAMConfirmMessage("secret", s.selectedSecret.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	case "version-enable", "version-disable", "version-destroy":
		if s.selectedVersion == nil {
			return "Error: No version selected"
		}
		verb := strings.TrimPrefix(s.pendingAction, "version-")
		label := fmt.Sprintf("version %s", s.selectedVersion.Name)
		if verb == "destroy" {
			return components.RenderConfirmationWithMessage(
				"destroy",
				label,
				"secret version",
				fmt.Sprintf("Are you sure you want to DESTROY %s of secret %s? This irrecoverably deletes its payload.", label, s.selectedSecretName()),
			)
		}
		return components.RenderConfirmation(verb, label, "secret version")
	case "add-version":
		if s.selectedSecret == nil {
			return "Error: No secret selected"
		}
		return components.RenderConfirmationWithMessage(
			"add-version",
			s.selectedSecret.Name,
			"secret",
			fmt.Sprintf("Add new version to secret %s?", s.selectedSecret.Name),
		)
	default:
		return "Error: No pending action"
	}
}

// selectedSecretName returns the currently selected secret's name, or an
// empty string if none is selected — used only for confirmation-dialog text.
func (s *Service) selectedSecretName() string {
	if s.selectedSecret == nil {
		return ""
	}
	return s.selectedSecret.Name
}

func (s *Service) renderListView() string {
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
	)

	if len(s.secrets) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			breadcrumb,
			s.filter.View(),
			components.EmptyState("secrets"),
		)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		s.filter.View(),
		s.table.View(),
	)
}

func (s *Service) renderDetailView() string {
	if s.selectedSecret == nil {
		return "No secret selected"
	}

	sec := s.selectedSecret

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		sec.Name,
	)

	// Format labels
	labelStr := "-"
	if len(sec.Labels) > 0 {
		var labels []string
		for k, v := range sec.Labels {
			labels = append(labels, fmt.Sprintf("%s=%s", k, v))
		}
		labelStr = fmt.Sprintf("%v", labels)
	}

	s.detailList.Title = "Secret Details"
	s.detailList.SetRows([]components.KeyValue{
		{Key: "Name", Value: sec.Name},
		{Key: "Replication", Value: sec.Replication},
		{Key: "Labels", Value: labelStr},
		{Key: "Created", Value: sec.CreateTime.Local().Format("2006-01-02 15:04:05")},
		{Key: "Resource Name", Value: sec.FullName},
	})
	s.detailList.FooterHint = "↑↓ Select | y Copy | v Versions | q Back"

	// Security note
	note := lipgloss.NewStyle().
		Foreground(lipgloss.Color("241")).
		Italic(true).
		Render("Note: Secret values are not displayed for security reasons.")

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.detailList.View(),
		"",
		note,
	)
}

func (s *Service) renderVersionsView() string {
	if s.selectedSecret == nil {
		return "No secret selected"
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedSecret.Name,
		"Versions",
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.versionTable.View(),
	)
}

func (s *Service) renderVersionDetailView() string {
	if s.selectedSecret == nil || s.selectedVersion == nil {
		return "No version selected"
	}

	ver := s.selectedVersion

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project: %s", s.projectID),
		s.Name(),
		s.selectedSecret.Name,
		"Versions",
		ver.Name,
	)

	rows := []components.KeyValue{
		{Key: "Version", Value: ver.Name},
		{Key: "State", Value: ver.State},
		{Key: "Created", Value: ver.CreateTime.Local().Format("2006-01-02 15:04:05")},
		{Key: "Resource Name", Value: ver.FullName},
	}

	var valueBlock string
	switch {
	case s.revealErr != nil:
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorError).
			Render(fmt.Sprintf("Failed to reveal value: %v", s.revealErr))
	case s.revealed:
		// Rendered as its own JoinVertical block, not a DetailList row —
		// a row is a single formatted line, which mangles/blanks any
		// secret value containing newlines (JSON keys, PEM certs, .env
		// files). Copying it is handled by the dedicated "Y" key instead
		// of the row-cursor "y" path.
		warning := lipgloss.NewStyle().
			Foreground(styles.ColorWarning).
			Bold(true).
			Render("[!] VISIBLE - press v to hide, Y to copy")
		value := lipgloss.NewStyle().
			Foreground(styles.ColorTextPrimary).
			Render(s.revealedValue)
		valueBlock = lipgloss.JoinVertical(lipgloss.Left, warning, "", value)
	default:
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorTextMuted).
			Italic(true).
			Render("Value hidden. Press v to reveal (fetches from Secret Manager).")
	}

	s.versionDetailList.Title = "Secret Version Details"
	s.versionDetailList.SetRows(rows)
	s.versionDetailList.FooterHint = "↑↓ Select | y Copy Field | v Reveal/Hide | q Back"

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		s.versionDetailList.View(),
		"",
		valueBlock,
	)
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	secretID := s.createForm.Value("Secret ID")
	replication := s.createForm.Value("Replication")
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret",
			Name: secretID, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateSecret(s.projectID, secretID, replication)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Secret %s created", secretID)}
	}
}

// submitUpdateCmd fires the UpdateSecretLabels API call, parsing the
// update-form's comma-separated key=value pairs into a labels map.
func (s *Service) submitUpdateCmd(sec Secret) tea.Cmd {
	raw := s.updateForm.Value("Labels (key=value,key2=value2)")
	labels := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			labels[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret",
			Name: sec.Name, Action: "update",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.UpdateSecretLabels(sec.FullName, labels)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating secret %s...", sec.Name)}
	}
}

// deleteSecretCmd triggers deletion of the given secret
func (s *Service) deleteSecretCmd(sec Secret) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret",
			Name: sec.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteSecret(sec.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting secret %s...", sec.Name)}
	}
}

// addVersionCmd adds a new version with the given plaintext value to the
// given secret. This is the data-plane "add version" operation
// (`gcloud secrets versions add`) — additive/non-destructive, but still
// gated behind the standard confirmation dialog since it writes real data.
func (s *Service) addVersionCmd(sec Secret, value string) tea.Cmd {
	return func() tea.Msg {
		var versionName string
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret",
			Name: sec.Name, Action: "add-version",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			var apiErr error
			versionName, apiErr = s.client.AddVersion(sec.FullName, value)
			return apiErr
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Added version %s to secret %s", versionName, sec.Name)}
	}
}

// enableVersionCmd triggers re-enabling the given secret version
func (s *Service) enableVersionCmd(ver SecretVersion) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret version",
			Name: ver.Name, Action: "enable",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.EnableVersion(ver.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Enabling version %s...", ver.Name)}
	}
}

// disableVersionCmd triggers disabling the given secret version
func (s *Service) disableVersionCmd(ver SecretVersion) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret version",
			Name: ver.Name, Action: "disable",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DisableVersion(ver.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Disabling version %s...", ver.Name)}
	}
}

// destroyVersionCmd triggers irrecoverably destroying the given secret version
func (s *Service) destroyVersionCmd(ver SecretVersion) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			Service: s.ShortName(), ProjectID: s.projectID, Resource: "secret version",
			Name: ver.Name, Action: "destroy",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DestroyVersion(ver.FullName)
		})
		if err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Destroying version %s...", ver.Name)}
	}
}

// -----------------------------------------------------------------------------
// Data Fetching
// -----------------------------------------------------------------------------

func (s *Service) fetchSecretsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("secrets:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(cacheKey); found {
				if secrets, ok := val.([]Secret); ok {
					return secretsMsg(secrets)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		secrets, err := s.client.ListSecrets(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(cacheKey, secrets, CacheTTL)
		}

		return secretsMsg(secrets)
	}
}

func (s *Service) fetchVersionsCmd(secretName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}

		versions, err := s.client.ListVersions(secretName)
		if err != nil {
			return errMsg(err)
		}

		return versionsMsg(versions)
	}
}

// fetchRevealCmd fetches the plaintext value for a specific secret version.
// This is only ever invoked from the explicit "v" (reveal) keypress in
// ViewVersionDetail — never from Init/Refresh/tick or as a side effect of
// simply navigating to a version. The fetched value is never logged (see
// utils.Log usage elsewhere in this package — there is none here) and is
// only ever held in-memory in s.revealedValue, cleared on Hide or on
// leaving the view.
func (s *Service) fetchRevealCmd(versionFullName string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return revealErrMsg{err: fmt.Errorf("client not initialized")}
		}

		value, err := s.client.AccessVersion(versionFullName)
		if err != nil {
			return revealErrMsg{err: err}
		}

		return revealedMsg(value)
	}
}

// -----------------------------------------------------------------------------
// Table Updates
// -----------------------------------------------------------------------------

func (s *Service) updateTable(secrets []Secret) {
	rows := make([]table.Row, len(secrets))
	for i, sec := range secrets {
		// Format labels for display
		labelStr := "-"
		if len(sec.Labels) > 0 {
			count := len(sec.Labels)
			if count == 1 {
				for k, v := range sec.Labels {
					labelStr = fmt.Sprintf("%s=%s", k, v)
				}
			} else {
				labelStr = fmt.Sprintf("%d labels", count)
			}
		}

		rows[i] = table.Row{
			sec.Name,
			sec.Replication,
			labelStr,
			sec.CreateTime.Local().Format("2006-01-02 15:04"),
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateVersionTable(versions []SecretVersion) {
	rows := make([]table.Row, len(versions))
	for i, v := range versions {
		rows[i] = table.Row{
			v.Name,
			versionStateText(v.State),
			v.CreateTime.Local().Format("2006-01-02 15:04:05"),
		}
	}
	s.versionTable.SetRows(rows)
}

// versionStateText renders a version's state as a plain icon+text string for
// the versions StandardTable. Unlike components.RenderStatus (a
// padding+background-colored badge whose ANSI escapes bubbles/table's
// fixed-width column truncation doesn't account for), this stays plain so
// column alignment holds regardless of state text length.
func versionStateText(state string) string {
	icon := components.IconUnknown
	switch components.CategorizeStatus(state) {
	case components.StatusRunning:
		icon = components.IconRunning
	case components.StatusStopped:
		icon = components.IconStopped
	case components.StatusPending:
		icon = components.IconPending
	}
	return icon + " " + strings.ToUpper(strings.TrimSpace(state))
}

func (s *Service) getCurrentSecrets() []Secret {
	return s.getFilteredSecrets(s.secrets, s.filter.Value())
}

func (s *Service) getFilteredSecrets(secrets []Secret, query string) []Secret {
	if query == "" {
		return secrets
	}
	return components.FilterSlice(secrets, query, func(sec Secret, q string) bool {
		// Build label string for search
		var labelStr string
		for k, v := range sec.Labels {
			labelStr += k + "=" + v + " "
		}
		return components.ContainsMatch(sec.Name, sec.Replication, labelStr)(q)
	})
}
