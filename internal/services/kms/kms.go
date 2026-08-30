package kms

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

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewRings ViewState = iota
	ViewKeys
	ViewCreate
	ViewUpdate
	ViewConfirmation
	ViewIAM
	ViewIAMForm
	ViewPublicKey
	ViewVersions
)

// newKeyUpdateForm builds the FormModel for updating a crypto key's
// automatic rotation period (e.g. "7776000s" for 90 days), seeded with its
// current value if set. Key-version lifecycle operations (set-primary-version,
// enable/disable/destroy/restore) live in the separate versions view (see
// ViewVersions) rather than this Update flow.
func newKeyUpdateForm(key CryptoKey) components.FormModel {
	return components.NewForm("Update Crypto Key: "+key.Name, []components.FormField{
		{Label: "Rotation Period (Go duration, e.g. 2160h)", Default: rotationDefault(key.RotationPeriod), Required: true, Validate: func(v string) string {
			d, err := time.ParseDuration(v)
			if err != nil || d <= 0 {
				return "must be a valid positive Go duration, e.g. 2160h"
			}
			return ""
		}},
	})
}

func rotationDefault(existing string) string {
	if existing == "" {
		return "2160h0m0s"
	}
	return existing
}

type ringsMsg []KeyRing
type keysMsg []CryptoKey
type errMsg error

// versionsMsg carries the result of a ListCryptoKeyVersions fetch.
type versionsMsg struct {
	versions []CryptoKeyVersion
	err      error
}

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetKeyRingIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// publicKeyMsg carries the result of a GetPublicKey fetch, triggered only by
// the explicit "k" keypress on an asymmetric key — never automatically.
type publicKeyMsg struct {
	pem string
	err error
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	ringTable *components.StandardTable
	keyTable  *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[KeyRing]

	rings   []KeyRing
	keys    []CryptoKey
	spinner components.SpinnerModel
	err     error

	viewState    ViewState
	selectedRing *KeyRing
	selectedKey  *CryptoKey

	// Create form state. createReturnView records whether "n" was pressed
	// from ViewRings (create a key ring) or ViewKeys (create a key inside
	// the currently selected ring).
	createForm       components.FormModel
	createReturnView ViewState

	// Update form state
	updateForm components.FormModel

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// IAM: current bindings for the selected key ring or crypto key (see
	// iamOnKey), and the add-binding form. pendingIAMRole/pendingIAMMember
	// are captured at form-submit time so the confirmation dialog and the
	// actual API call use the same values regardless of what the form
	// fields hold later.
	iamBindings      []IAMBinding
	iamForm          components.FormModel
	pendingIAMRole   string
	pendingIAMMember string
	// iamOnKey is true when the IAM view/form/bindings currently in scope
	// target the selected crypto key rather than the selected key ring.
	iamOnKey bool

	// Versions: the selected crypto key's versions, for the key-version
	// lifecycle operations (set-primary/enable/disable/destroy/restore).
	versions        []CryptoKeyVersion
	selectedVersion *CryptoKeyVersion
	versionTable    *components.StandardTable

	// Public key state — session-only, holds the PEM fetched for the
	// currently selected asymmetric key's version 1 (see api.go GetPublicKey
	// for the "version 1 only" simplification). Cleared whenever the user
	// leaves the view.
	publicKeyPem string
	publicKeyErr error

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	ringColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Location", Width: 15},
		{Title: "Created", Width: 20},
	}
	rt := components.NewStandardTable(ringColumns)

	keyColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Purpose", Width: 20},
		{Title: "Algorithm", Width: 28},
		{Title: "Protection", Width: 10},
		{Title: "State", Width: 12},
		{Title: "Rotation", Width: 12},
	}
	kt := components.NewStandardTable(keyColumns)

	versionColumns := []table.Column{
		{Title: "Version", Width: 12},
		{Title: "State", Width: 20},
		{Title: "Primary", Width: 10},
		{Title: "Created", Width: 20},
	}
	vt := components.NewStandardTable(versionColumns)

	svc := &Service{
		ringTable:    rt,
		keyTable:     kt,
		versionTable: vt,
		filter:       components.NewFilterWithPlaceholder("Filter key rings..."),
		spinner:      components.NewSpinner(),
		viewState:    ViewRings,
		cache:        cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredRings, svc.updateRingTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud KMS"
}

func (s *Service) ShortName() string {
	return "kms"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewRings:
		return "r:Refresh  /:Filter  n:New Key Ring  Ent:Keys  i:IAM"
	case ViewKeys:
		return "Esc/q:Back  n:New Key  u:Update  d:Delete  v:Versions  i:IAM  k:Public Key"
	case ViewVersions:
		return "p:Set Primary  e:Enable  x:Disable  d:Destroy  R:Restore  Esc/q:Back"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewIAM:
		return "a:Add Binding  q/Esc:Back"
	case ViewIAMForm:
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	case ViewPublicKey:
		return "q/Esc:Back"
	case ViewCreate, ViewUpdate:
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

// Reinit reinitializes the service with a new project ID
func (s *Service) Reinit(ctx context.Context, projectID string) error {
	s.Reset()
	return s.InitService(ctx, projectID)
}

func (s *Service) Init() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchRingsCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	if s.viewState == ViewKeys && s.selectedRing != nil {
		return tea.Batch(
			s.spinner.Start(""),
			s.fetchKeysCmd(s.selectedRing.FullName),
		)
	}
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchRingsCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewRings
	s.selectedRing = nil
	s.selectedKey = nil
	s.keys = nil
	s.versions = nil
	s.selectedVersion = nil
	s.iamOnKey = false
	s.err = nil
	s.ringTable.SetCursor(0)
	s.keyTable.SetCursor(0)
	s.versionTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewRings
}

func (s *Service) Focus() {
	s.ringTable.Focus()
}

func (s *Service) Blur() {
	s.ringTable.Blur()
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
		if s.viewState == ViewRings {
			return s, tea.Batch(s.fetchRingsCmd(false), s.tick())
		}
		return s, s.tick()

	case ringsMsg:
		s.spinner.Stop()
		s.rings = msg
		s.filterSession.Apply(s.rings)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case keysMsg:
		s.spinner.Stop()
		s.keys = msg
		s.updateKeyTable(msg)
		return s, nil

	case versionsMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.versions = msg.versions
		s.updateVersionTable(s.versions)
		s.viewState = ViewVersions
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

	case publicKeyMsg:
		s.spinner.Stop()
		s.publicKeyPem = msg.pem
		s.publicKeyErr = msg.err
		s.viewState = ViewPublicKey
		return s, nil

	case actionResultMsg:
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			resource, name := "key ring", ""
			if s.iamOnKey && s.selectedKey != nil {
				resource, name = "crypto key", s.selectedKey.Name
			} else if s.selectedRing != nil {
				name = s.selectedRing.Name
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  resource,
					Name:      name,
					Action:    "grant",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  resource,
				Name:      name,
				Action:    "grant",
				Status:    core.JobSuccess,
			})
			if s.iamOnKey && s.selectedKey != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchKeyIAMCmd(*s.selectedKey),
				)
			}
			if s.selectedRing != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedRing),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if isVersionAction(s.pendingAction) {
			verb := strings.TrimSuffix(s.pendingAction, "-version")
			s.pendingAction = ""
			versionID := ""
			if s.selectedVersion != nil {
				versionID = s.selectedVersion.VersionID
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "crypto key version",
					Name:      versionID,
					Action:    verb,
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "crypto key version",
				Name:      versionID,
				Action:    verb,
				Status:    core.JobSuccess,
			})
			s.viewState = ViewVersions
			if s.selectedKey == nil {
				return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			}
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.spinner.Start(""),
				s.fetchVersionsCmd(*s.selectedKey),
			)
		}
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			name := ""
			if s.selectedKey != nil {
				name = s.selectedKey.Name
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "crypto key",
					Name:      name,
					Action:    "delete",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "crypto key",
				Name:      name,
				Action:    "delete",
				Status:    core.JobSuccess,
			})
			s.selectedKey = nil
			s.viewState = ViewKeys
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		wasUpdate := s.viewState == ViewUpdate
		wasKeyRing := s.createReturnView == ViewRings
		if msg.err != nil {
			if wasUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
				name := ""
				if s.selectedKey != nil {
					name = s.selectedKey.Name
				}
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "crypto key",
					Name:      name,
					Action:    "update",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
			} else {
				s.createForm.SubmitErr = msg.err.Error()
				resource, name := "crypto key", s.createForm.Value("Key ID")
				if wasKeyRing {
					resource, name = "key ring", s.createForm.Value("Key Ring ID")
				}
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  resource,
					Name:      name,
					Action:    "create",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
			}
			return s, nil
		}
		if wasUpdate {
			name := ""
			if s.selectedKey != nil {
				name = s.selectedKey.Name
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "crypto key",
				Name:      name,
				Action:    "update",
				Status:    core.JobSuccess,
			})
			s.viewState = ViewKeys
		} else {
			resource, name := "crypto key", s.createForm.Value("Key ID")
			if wasKeyRing {
				resource, name = "key ring", s.createForm.Value("Key Ring ID")
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  resource,
				Name:      name,
				Action:    "create",
				Status:    core.JobSuccess,
			})
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
		s.ringTable.HandleWindowSizeDefault(msg)
		s.keyTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		switch s.viewState {
		case ViewRings:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.ringTable.Update(msg)
			s.ringTable = updatedTable
			return s, cmd
		case ViewKeys:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.keyTable.Update(msg)
			s.keyTable = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		return s.handleKeyMsg(msg)
	}

	return s, nil
}

func (s *Service) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

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
			s.viewState = ViewKeys
			return s, nil
		}
		if result.Submitted && s.selectedKey != nil {
			return s, s.submitUpdateCmd(*s.selectedKey)
		}
		return s, formCmd
	}

	if s.viewState == ViewIAMForm {
		result, formCmd := s.iamForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewIAM
			return s, nil
		}
		if result.Submitted && s.selectedRing != nil {
			s.pendingIAMRole = s.iamForm.Value("Role")
			s.pendingIAMMember = s.iamForm.Value("Member")
			s.pendingAction = "grant"
			s.actionSource = ViewIAM
			s.viewState = ViewConfirmation
			return s, nil
		}
		return s, formCmd
	}

	if s.viewState == ViewPublicKey {
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewKeys
			s.publicKeyPem = ""
			s.publicKeyErr = nil
			return s, nil
		}
		return s, nil
	}

	if s.viewState == ViewIAM {
		switch msg.String() {
		case "q", "esc":
			if s.iamOnKey {
				s.viewState = ViewKeys
			} else {
				s.viewState = ViewRings
			}
			return s, nil
		case "a":
			if s.iamOnKey && s.selectedKey != nil {
				s.iamForm = components.NewIAMAddBindingForm(s.selectedKey.Name)
				s.viewState = ViewIAMForm
			} else if !s.iamOnKey && s.selectedRing != nil {
				s.iamForm = components.NewIAMAddBindingForm(s.selectedRing.Name)
				s.viewState = ViewIAMForm
			}
			return s, nil
		}
	}

	if s.viewState == ViewVersions {
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewKeys
			s.versions = nil
			s.selectedVersion = nil
			return s, nil
		case "p": // Set as primary version
			if s.selectedKey != nil {
				if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
					s.selectedVersion = &s.versions[idx]
					s.pendingAction = "set-primary"
					s.actionSource = ViewVersions
					s.viewState = ViewConfirmation
				}
			}
			return s, nil
		case "e": // Enable
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.pendingAction = "enable-version"
				s.actionSource = ViewVersions
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "x": // Disable
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.pendingAction = "disable-version"
				s.actionSource = ViewVersions
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "d": // Destroy
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.pendingAction = "destroy-version"
				s.actionSource = ViewVersions
				s.viewState = ViewConfirmation
			}
			return s, nil
		case "R": // Restore
			if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.versions) {
				s.selectedVersion = &s.versions[idx]
				s.pendingAction = "restore-version"
				s.actionSource = ViewVersions
				s.viewState = ViewConfirmation
			}
			return s, nil
		}
		var updatedTable *components.StandardTable
		updatedTable, cmd = s.versionTable.Update(msg)
		s.versionTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewRings {
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
			s.createReturnView = ViewRings
			s.createForm = components.NewForm("New Key Ring", []components.FormField{
				{Label: "Key Ring ID", Placeholder: "my-keyring", Required: true},
				{Label: "Location", Placeholder: "us-central1", Required: true},
			})
			s.viewState = ViewCreate
			return s, nil
		case "enter":
			rings := s.getFilteredRings(s.rings, s.filter.Value())
			if idx := s.ringTable.Cursor(); idx >= 0 && idx < len(rings) {
				s.selectedRing = &rings[idx]
				s.viewState = ViewKeys
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchKeysCmd(s.selectedRing.FullName),
				)
			}
		case "i":
			rings := s.getFilteredRings(s.rings, s.filter.Value())
			if idx := s.ringTable.Cursor(); idx >= 0 && idx < len(rings) {
				s.selectedRing = &rings[idx]
				s.iamOnKey = false
				return s, tea.Batch(s.fetchIAMCmd(*s.selectedRing), s.spinner.Start(""))
			}
			return s, nil
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.ringTable.Update(msg)
		s.ringTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewKeys {
		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "n":
			if s.selectedRing != nil {
				s.createReturnView = ViewKeys
				s.createForm = components.NewForm("New Crypto Key", []components.FormField{
					{Label: "Key ID", Placeholder: "my-key", Required: true},
					{Label: "Purpose", Default: "ENCRYPT_DECRYPT"},
					{Label: "Algorithm", Default: "GOOGLE_SYMMETRIC_ENCRYPTION"},
				})
				s.viewState = ViewCreate
			}
			return s, nil
		case "esc", "q":
			s.viewState = ViewRings
			s.selectedRing = nil
			s.keys = nil
			return s, nil
		case "u":
			if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
				s.selectedKey = &s.keys[idx]
				s.updateForm = newKeyUpdateForm(*s.selectedKey)
				s.viewState = ViewUpdate
				return s, nil
			}
		case "d":
			if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
				s.selectedKey = &s.keys[idx]
				s.pendingAction = "delete"
				s.actionSource = ViewKeys
				s.viewState = ViewConfirmation
				return s, nil
			}
		case "k":
			if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
				key := s.keys[idx]
				s.selectedKey = &key
				if key.Purpose != "ASYMMETRIC_SIGN" && key.Purpose != "ASYMMETRIC_DECRYPT" {
					return s, func() tea.Msg {
						return core.ToastMsg{
							Message: "Get public key only applies to asymmetric keys (ASYMMETRIC_SIGN/ASYMMETRIC_DECRYPT), not " + key.Purpose,
							Type:    core.ToastError,
						}
					}
				}
				if s.selectedRing == nil {
					return s, nil
				}
				return s, tea.Batch(s.spinner.Start(""), s.fetchPublicKeyCmd(*s.selectedRing, key))
			}
		case "v":
			if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
				key := s.keys[idx]
				s.selectedKey = &key
				return s, tea.Batch(s.spinner.Start(""), s.fetchVersionsCmd(key))
			}
		case "i":
			if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
				key := s.keys[idx]
				s.selectedKey = &key
				s.iamOnKey = true
				return s, tea.Batch(s.fetchKeyIAMCmd(key), s.spinner.Start(""))
			}
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.keyTable.Update(msg)
		s.keyTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewConfirmation {
		switch msg.String() {
		case "y", "enter":
			var actionCmd tea.Cmd
			switch {
			case s.pendingAction == "delete" && s.selectedKey != nil && s.selectedRing != nil:
				actionCmd = s.deleteKeyCmd(*s.selectedKey)
			case s.pendingAction == "grant" && s.iamOnKey && s.selectedKey != nil && s.selectedRing != nil:
				actionCmd = s.addKeyIAMBindingCmd(*s.selectedRing, *s.selectedKey, s.pendingIAMRole, s.pendingIAMMember)
			case s.pendingAction == "grant" && !s.iamOnKey && s.selectedRing != nil:
				actionCmd = s.addIAMBindingCmd(*s.selectedRing, s.pendingIAMRole, s.pendingIAMMember)
			case s.pendingAction == "set-primary" && s.selectedKey != nil && s.selectedVersion != nil:
				actionCmd = s.setPrimaryVersionCmd(*s.selectedKey, *s.selectedVersion)
			case s.pendingAction == "enable-version" && s.selectedVersion != nil:
				actionCmd = s.enableVersionCmd(*s.selectedVersion)
			case s.pendingAction == "disable-version" && s.selectedVersion != nil:
				actionCmd = s.disableVersionCmd(*s.selectedVersion)
			case s.pendingAction == "destroy-version" && s.selectedVersion != nil:
				actionCmd = s.destroyVersionCmd(*s.selectedVersion)
			case s.pendingAction == "restore-version" && s.selectedVersion != nil:
				actionCmd = s.restoreVersionCmd(*s.selectedVersion)
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

// -----------------------------------------------------------------------------
// Views
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Key Rings")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewKeys {
		return s.renderKeysView()
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

	if s.viewState == ViewPublicKey {
		return s.renderPublicKeyView()
	}

	if s.viewState == ViewVersions {
		return s.renderVersionsView()
	}

	return s.renderListView()
}

// renderIAMView renders the current IAM policy bindings for the selected
// key ring or crypto key (see iamOnKey), the safety-net read step before
// allowing an add-binding write.
func (s *Service) renderIAMView() string {
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	if s.iamOnKey {
		if s.selectedKey == nil || s.selectedRing == nil {
			return "Error: No key selected"
		}
		breadcrumb := components.Breadcrumb(
			fmt.Sprintf("Project %s", s.projectID),
			s.Name(),
			"Key Rings",
			s.selectedRing.Name,
			s.selectedKey.Name,
			"IAM",
		)
		return components.RenderIAMBindings(breadcrumb, s.selectedKey.Name, rows)
	}
	if s.selectedRing == nil {
		return "Error: No key ring selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
		s.selectedRing.Name,
		"IAM",
	)
	return components.RenderIAMBindings(breadcrumb, s.selectedRing.Name, rows)
}

// renderVersionsView renders the selected crypto key's versions.
func (s *Service) renderVersionsView() string {
	if s.selectedRing == nil || s.selectedKey == nil {
		return "Error: No key selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
		s.selectedRing.Name,
		s.selectedKey.Name,
		"Versions",
	)
	if len(s.versions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("versions"))
	}
	hint := styles.HelpStyle.Render("p Set Primary  |  e Enable  |  x Disable  |  d Destroy  |  R Restore  |  q Back")
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.versionTable.View(), "", hint)
}

// renderPublicKeyView renders the PEM-encoded public key fetched for the
// currently selected asymmetric crypto key's version 1 (see api.go
// GetPublicKey for the "version 1 only" simplification), mirroring the
// Secret Manager "reveal value" display pattern.
func (s *Service) renderPublicKeyView() string {
	if s.selectedRing == nil || s.selectedKey == nil {
		return "Error: No key selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
		s.selectedRing.Name,
		s.selectedKey.Name,
		"Public Key",
	)

	var valueBlock string
	if s.publicKeyErr != nil {
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorError).
			Render(fmt.Sprintf("Failed to get public key: %v", s.publicKeyErr))
	} else {
		valueBlock = lipgloss.NewStyle().
			Foreground(styles.ColorTextPrimary).
			Render(s.publicKeyPem)
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		breadcrumb,
		"",
		fmt.Sprintf("Public key for %s (version 1):", s.selectedKey.Name),
		"",
		valueBlock,
	)
}

// renderConfirmation renders the confirmation dialog for the pending
// action: key delete, IAM grant, or a key-version lifecycle operation.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "grant" {
		return s.renderIAMConfirmation()
	}
	if isVersionAction(s.pendingAction) {
		return s.renderVersionConfirmation()
	}
	if s.selectedKey == nil {
		return "Error: No key selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedKey.Name,
		"crypto key",
		fmt.Sprintf("Are you sure you want to DELETE key %s? This only succeeds if every key version has already been destroyed (see the Versions view — press 'v' on a key — for destroying individual versions first).", s.selectedKey.Name),
	)
}

// renderIAMConfirmation renders the IAM-grant confirmation dialog for the
// selected key ring or crypto key (see iamOnKey).
func (s *Service) renderIAMConfirmation() string {
	if s.iamOnKey {
		if s.selectedKey == nil {
			return "Error: No key selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedKey.Name,
			"crypto key",
			components.IAMConfirmMessage("crypto key", s.selectedKey.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	if s.selectedRing == nil {
		return "Error: No key ring selected"
	}
	return components.RenderConfirmationWithMessage(
		"grant",
		s.selectedRing.Name,
		"key ring",
		components.IAMConfirmMessage("key ring", s.selectedRing.Name, s.pendingIAMRole, s.pendingIAMMember),
	)
}

// renderVersionConfirmation renders the confirmation dialog for a
// key-version lifecycle action (set-primary/enable/disable/destroy/restore).
func (s *Service) renderVersionConfirmation() string {
	if s.selectedVersion == nil {
		return "Error: No version selected"
	}
	verb := strings.TrimSuffix(s.pendingAction, "-version")
	if verb == "set-primary" {
		return components.RenderConfirmationWithMessage(
			"set-primary",
			s.selectedVersion.VersionID,
			"crypto key version",
			fmt.Sprintf("Set version %s as the primary version?", s.selectedVersion.VersionID),
		)
	}
	message := fmt.Sprintf("%s version %s?", strings.ToUpper(verb[:1])+verb[1:], s.selectedVersion.VersionID)
	if verb == "destroy" {
		message += " This schedules the key material for destruction after a ~24h grace period, during which it can still be restored."
	}
	return components.RenderConfirmationWithMessage(verb, s.selectedVersion.VersionID, "crypto key version", message)
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.ringTable.View())
	return content.String()
}

func (s *Service) renderKeysView() string {
	ringName := ""
	if s.selectedRing != nil {
		ringName = s.selectedRing.Name
	}

	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Key Rings",
		ringName,
	))
	content.WriteString("\n")
	content.WriteString(s.keyTable.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if s.createReturnView == ViewRings {
			keyRingID := s.createForm.Value("Key Ring ID")
			location := s.createForm.Value("Location")
			if err := s.client.CreateKeyRing(s.projectID, location, keyRingID); err != nil {
				return actionResultMsg{err: err}
			}
			return actionResultMsg{msg: fmt.Sprintf("Key ring %s created in %s", keyRingID, location)}
		}

		if s.selectedRing == nil {
			return actionResultMsg{err: fmt.Errorf("no key ring selected")}
		}
		keyID := s.createForm.Value("Key ID")
		purpose := s.createForm.Value("Purpose")
		algorithm := s.createForm.Value("Algorithm")
		if err := s.client.CreateCryptoKey(s.selectedRing.FullName, keyID, purpose, algorithm); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Key %s created in %s", keyID, s.selectedRing.Name)}
	}
}

// submitUpdateCmd fires the UpdateCryptoKeyRotationSchedule API call using
// the current update-form value.
func (s *Service) submitUpdateCmd(key CryptoKey) tea.Cmd {
	raw := s.updateForm.Value("Rotation Period (Go duration, e.g. 2160h)")
	period, err := time.ParseDuration(raw)
	if err != nil || period <= 0 {
		period = 2160 * time.Hour
	}
	return func() tea.Msg {
		if s.client == nil || s.selectedRing == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateCryptoKeyRotationSchedule(s.selectedRing.FullName, key.Name, period); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating rotation schedule for key %s...", key.Name)}
	}
}

// deleteKeyCmd triggers deletion of the given crypto key
func (s *Service) deleteKeyCmd(key CryptoKey) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil || s.selectedRing == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteCryptoKey(s.selectedRing.FullName, key.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting key %s...", key.Name)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a key ring.
func (s *Service) fetchIAMCmd(ring KeyRing) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetKeyRingIAMPolicy(ring.FullName)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// fetchPublicKeyCmd fetches the PEM-encoded public key for an asymmetric
// crypto key's version 1. Only ever invoked from the explicit "k" keypress
// on an asymmetric key in ViewKeys — never automatically.
func (s *Service) fetchPublicKeyCmd(ring KeyRing, key CryptoKey) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return publicKeyMsg{err: fmt.Errorf("client not initialized")}
		}
		pem, err := s.client.GetPublicKey(ring.FullName, key.Name)
		if err != nil {
			return publicKeyMsg{err: err}
		}
		return publicKeyMsg{pem: pem}
	}
}

// addIAMBindingCmd grants role to member on the given key ring.
func (s *Service) addIAMBindingCmd(ring KeyRing, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddKeyRingIAMBinding(ring.FullName, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on key ring %s", role, member, ring.Name)}
	}
}

// fetchKeyIAMCmd fetches the current IAM policy for a crypto key.
func (s *Service) fetchKeyIAMCmd(key CryptoKey) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil || s.selectedRing == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetCryptoKeyIAMPolicy(cryptoKeyFullName(*s.selectedRing, key))
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addKeyIAMBindingCmd grants role to member on the selected crypto key.
func (s *Service) addKeyIAMBindingCmd(ring KeyRing, key CryptoKey, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddCryptoKeyIAMBinding(cryptoKeyFullName(ring, key), role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on key %s", role, member, key.Name)}
	}
}

// fetchVersionsCmd lists the versions of the selected crypto key.
func (s *Service) fetchVersionsCmd(key CryptoKey) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil || s.selectedRing == nil {
			return versionsMsg{err: fmt.Errorf("client not initialized")}
		}
		versions, err := s.client.ListCryptoKeyVersions(cryptoKeyFullName(*s.selectedRing, key))
		if err != nil {
			return versionsMsg{err: err}
		}
		return versionsMsg{versions: versions}
	}
}

// isVersionAction reports whether action is one of the key-version
// lifecycle pendingActions, all of which route through the same
// actionResultMsg handling (return to ViewVersions, refetch versions).
func isVersionAction(action string) bool {
	switch action {
	case "set-primary", "enable-version", "disable-version", "destroy-version", "restore-version":
		return true
	}
	return false
}

// setPrimaryVersionCmd sets version as the selected crypto key's primary version.
func (s *Service) setPrimaryVersionCmd(key CryptoKey, version CryptoKeyVersion) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil || s.selectedRing == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.SetPrimaryVersion(cryptoKeyFullName(*s.selectedRing, key), version.VersionID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Set version %s as primary for %s", version.VersionID, key.Name)}
	}
}

// enableVersionCmd re-enables a disabled crypto key version.
func (s *Service) enableVersionCmd(version CryptoKeyVersion) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.EnableCryptoKeyVersion(version.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Enabled version %s", version.VersionID)}
	}
}

// disableVersionCmd disables a crypto key version.
func (s *Service) disableVersionCmd(version CryptoKeyVersion) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DisableCryptoKeyVersion(version.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Disabled version %s", version.VersionID)}
	}
}

// destroyVersionCmd schedules a crypto key version for destruction.
func (s *Service) destroyVersionCmd(version CryptoKeyVersion) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DestroyCryptoKeyVersion(version.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Destruction scheduled for version %s", version.VersionID)}
	}
}

// restoreVersionCmd undoes a pending destroy on a crypto key version.
func (s *Service) restoreVersionCmd(version CryptoKeyVersion) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.RestoreCryptoKeyVersion(version.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Restored version %s", version.VersionID)}
	}
}

// cryptoKeyFullName builds a crypto key's full resource name from its key
// ring and short key name, matching the (keyRingFullName, keyID) signature
// CreateCryptoKey/DeleteCryptoKey already use in api.go.
func cryptoKeyFullName(ring KeyRing, key CryptoKey) string {
	return fmt.Sprintf("%s/cryptoKeys/%s", ring.FullName, key.Name)
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchRingsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("kms_rings:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]KeyRing); ok {
					return ringsMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListKeyRings(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return ringsMsg(items)
	}
}

func (s *Service) fetchKeysCmd(ringName string) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("kms_keys:%s:%s", s.projectID, ringName)

		if s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]CryptoKey); ok {
					return keysMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		fullRingName := fmt.Sprintf("projects/%s/locations/%s/keyRings/%s", s.projectID, s.selectedRing.Location, ringName)
		items, err := s.client.ListCryptoKeys(fullRingName)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return keysMsg(items)
	}
}

func (s *Service) updateRingTable(items []KeyRing) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Location,
			item.CreateTime,
		}
	}
	s.ringTable.SetRows(rows)
}

func (s *Service) updateKeyTable(items []CryptoKey) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rotation := item.RotationPeriod
		if rotation == "" {
			rotation = "-"
		}
		rows[i] = table.Row{
			item.Name,
			item.Purpose,
			item.Algorithm,
			item.ProtectionLevel,
			item.State,
			rotation,
		}
	}
	s.keyTable.SetRows(rows)
}

func (s *Service) updateVersionTable(items []CryptoKeyVersion) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.VersionID,
			item.State,
			fmt.Sprintf("%t", item.Primary),
			item.CreateTime,
		}
	}
	s.versionTable.SetRows(rows)
}

// getFilteredRings returns filtered key rings based on the query string
func (s *Service) getFilteredRings(rings []KeyRing, query string) []KeyRing {
	if query == "" {
		return rings
	}
	return components.FilterSlice(rings, query, func(ring KeyRing, q string) bool {
		return components.ContainsMatch(ring.Name, ring.Location)(q)
	})
}
