package iam

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

const CacheTTL = 5 * time.Minute

// Tick message for background refresh
type tickMsg time.Time

// Service implements the generic Service interface for IAM
type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	// State
	accounts       []ServiceAccount
	policyBindings []PolicyMember
	spinner        components.SpinnerModel
	err            error

	// View State
	viewDetail      bool
	viewCreate      bool
	viewUpdate      bool
	viewConfirm     bool
	viewIAM         bool
	viewIAMForm     bool
	viewKeys        bool
	viewKeyCreate   bool
	viewUndelete    bool
	selectedAccount *ServiceAccount

	// pendingAction is "delete" then escalates to "delete-confirm2" — service
	// account delete immediately breaks any workload using that identity, so
	// this requires a double confirmation. Also "enable"/"disable"/"grant"/
	// "delete-key".
	pendingAction string

	createForm    components.FormModel
	updateForm    components.FormModel
	iamForm       components.FormModel
	undeleteForm  components.FormModel
	keyCreateForm components.FormModel

	// IAM State (the service account's own policy -- who can act as it)
	iamBindings      []IAMBinding
	pendingIAMRole   string
	pendingIAMMember string

	// Keys State
	keys        []ServiceAccountKey
	selectedKey *ServiceAccountKey
	keyTable    *components.StandardTable

	// Cache
	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	// Table Setup
	columns := []table.Column{
		{Title: "Display Name", Width: 30},
		{Title: "Email", Width: 40},
		{Title: "Status", Width: 10},
		{Title: "ID", Width: 25},
	}

	t := components.NewStandardTable(columns)

	keyColumns := []table.Column{
		{Title: "Key ID", Width: 30},
		{Title: "Valid After", Width: 22},
		{Title: "Valid Before", Width: 22},
		{Title: "Disabled", Width: 10},
	}
	keyTable := components.NewStandardTable(keyColumns)

	return &Service{
		table:    t,
		keyTable: keyTable,
		spinner:  components.NewSpinner(),
		cache:    cache,
	}
}

func (s *Service) Name() string {
	return "Identity & Access Management"
}

func (s *Service) ShortName() string {
	return "iam"
}

func (s *Service) HelpText() string {
	if s.viewCreate || s.viewUpdate || s.viewIAMForm || s.viewUndelete || s.viewKeyCreate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewConfirm {
		return "y:Confirm  n:Cancel"
	}
	if s.viewKeys {
		return "Esc/q:Back  n:Create Key  d:Delete Key"
	}
	if s.viewIAM {
		return "Esc/q:Back  a:Grant Role"
	}
	if s.viewDetail {
		return "Esc/q:Back  u:Update  E:Enable  D:Disable  d:Delete  i:IAM  K:Keys"
	}
	return "r:Refresh  n:New Service Account  U:Undelete  Ent:Detail"
}

// newAccountUpdateForm builds the FormModel for updating a service
// account's display name, seeded with its current value. Description is
// out of scope; undelete and the keys subgroup are covered separately (see
// undeleteAccountCmd / *KeyCmd below).
func newAccountUpdateForm(acc ServiceAccount) components.FormModel {
	return components.NewForm("Update Service Account: "+acc.Email, []components.FormField{
		{Label: "Display Name", Default: acc.DisplayName},
	})
}

func (s *Service) Focus() {
	s.table.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
}

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

// Msg types
type accountsMsg []ServiceAccount
type policyBindingsMsg []PolicyMember
type errMsg error

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetServiceAccountIAMPolicy fetch.
type iamPolicyMsg struct {
	err      error
	bindings []IAMBinding
}

// keysMsg carries the result of a ListServiceAccountKeys fetch.
type keysMsg struct {
	err  error
	keys []ServiceAccountKey
}

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchAccountsCmd(false), s.fetchPolicyCmd(false), s.tick())

	case accountsMsg:
		s.spinner.Stop()
		s.accounts = msg
		s.updateTable(msg)
		if s.selectedAccount != nil {
			for i := range s.accounts {
				if s.accounts[i].UniqueID == s.selectedAccount.UniqueID {
					s.selectedAccount = &s.accounts[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case policyBindingsMsg:
		s.policyBindings = msg
		return s, nil

	case iamPolicyMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.iamBindings = msg.bindings
		s.viewIAM = true
		return s, nil

	case keysMsg:
		s.spinner.Stop()
		if msg.err != nil {
			return s, func() tea.Msg {
				return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
			}
		}
		s.keys = msg.keys
		s.updateKeyTable(msg.keys)
		s.viewKeys = true
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction != "" {
			action := s.pendingAction
			s.pendingAction = ""
			acc := s.selectedAccount
			key := s.selectedKey
			if msg.err != nil {
				resource, name := "service account", ""
				if acc != nil {
					name = acc.Email
				}
				if (action == "delete-key" || action == "create-key") && key != nil {
					resource, name = "service account key", key.KeyID
				}
				jobAction := action
				if action == "delete-confirm2" {
					jobAction = "delete"
				} else if action == "delete-key" {
					jobAction = "delete"
				} else if action == "create-key" {
					jobAction = "create"
				}
				if action == "undelete" {
					name = s.undeleteForm.Value("Unique ID")
				}
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  resource,
					Name:      name,
					Action:    jobAction,
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			// Only a completed delete drops back to the list — a deleted
			// account no longer exists. Disable/enable leave the detail
			// view up so the refreshed status is visible in place.
			if action == "delete-confirm2" {
				if acc != nil {
					core.RecordJob(core.Job{
						Service:   s.ShortName(),
						ProjectID: s.projectID,
						Resource:  "service account",
						Name:      acc.Email,
						Action:    "delete",
						Status:    core.JobSuccess,
					})
				}
				s.viewDetail = false
				s.selectedAccount = nil
			}
			if action == "grant" && acc != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account",
					Name:      acc.Email,
					Action:    "grant",
					Status:    core.JobSuccess,
				})
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMPolicyCmd(*acc),
				)
			}
			if (action == "delete-key" || action == "create-key") && acc != nil {
				jobAction := "create"
				resourceName := ""
				if key != nil {
					resourceName = key.KeyID
				}
				if action == "delete-key" {
					jobAction = "delete"
				}
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account key",
					Name:      resourceName,
					Action:    jobAction,
					Status:    core.JobSuccess,
				})
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchKeysCmd(*acc),
				)
			}
			if action == "undelete" {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account",
					Name:      s.undeleteForm.Value("Unique ID"),
					Action:    "undelete",
					Status:    core.JobSuccess,
				})
				s.viewUndelete = false
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.Refresh(),
				)
			}
			if (action == "enable" || action == "disable") && acc != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account",
					Name:      acc.Email,
					Action:    action,
					Status:    core.JobSuccess,
				})
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		wasUpdate := s.viewUpdate
		if msg.err != nil {
			if wasUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
				name := ""
				if s.selectedAccount != nil {
					name = s.selectedAccount.Email
				}
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account",
					Name:      name,
					Action:    "update",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
			} else {
				s.createForm.SubmitErr = msg.err.Error()
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "service account",
					Name:      s.createForm.Value("Account ID"),
					Action:    "create",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
			}
			return s, nil
		}
		jobName := s.createForm.Value("Account ID")
		jobAction := "create"
		if wasUpdate {
			jobAction = "update"
			if s.selectedAccount != nil {
				jobName = s.selectedAccount.Email
			}
		}
		core.RecordJob(core.Job{
			Service:   s.ShortName(),
			ProjectID: s.projectID,
			Resource:  "service account",
			Name:      jobName,
			Action:    jobAction,
			Status:    core.JobSuccess,
		})
		s.viewCreate = false
		s.viewUpdate = false
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
		if !s.viewDetail && !s.viewCreate && !s.viewUpdate && !s.viewConfirm &&
			!s.viewIAM && !s.viewIAMForm && !s.viewKeys && !s.viewKeyCreate && !s.viewUndelete {
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewCreate {
			result, formCmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewCreate = false
				return s, nil
			}
			if result.Submitted {
				return s, s.submitCreateCmd()
			}
			return s, formCmd
		}
		if s.viewUpdate {
			result, formCmd := s.updateForm.Update(msg)
			if result.Cancelled {
				s.viewUpdate = false
				return s, nil
			}
			if result.Submitted && s.selectedAccount != nil {
				return s, s.submitUpdateCmd(*s.selectedAccount)
			}
			return s, formCmd
		}
		if s.viewIAMForm {
			result, formCmd := s.iamForm.Update(msg)
			if result.Cancelled {
				s.viewIAMForm = false
				s.viewIAM = true
				return s, nil
			}
			if result.Submitted && s.selectedAccount != nil {
				s.pendingIAMRole = s.iamForm.Value("Role")
				s.pendingIAMMember = s.iamForm.Value("Member")
				s.pendingAction = "grant"
				s.viewIAMForm = false
				s.viewConfirm = true
				return s, nil
			}
			return s, formCmd
		}
		if s.viewUndelete {
			result, formCmd := s.undeleteForm.Update(msg)
			if result.Cancelled {
				s.viewUndelete = false
				return s, nil
			}
			if result.Submitted {
				s.pendingAction = "undelete"
				return s, s.undeleteAccountCmd(s.undeleteForm.Value("Unique ID"))
			}
			return s, formCmd
		}
		if s.viewKeyCreate {
			result, formCmd := s.keyCreateForm.Update(msg)
			if result.Cancelled {
				s.viewKeyCreate = false
				s.viewKeys = true
				return s, nil
			}
			if result.Submitted && s.selectedAccount != nil {
				s.pendingAction = "create-key"
				outputPath := s.keyCreateForm.Value("Output File Path")
				s.viewKeyCreate = false
				return s, s.createKeyCmd(*s.selectedAccount, outputPath)
			}
			return s, formCmd
		}
		if s.viewConfirm {
			switch msg.String() {
			case "y", "enter":
				if s.pendingAction == "delete" {
					// First confirmation only escalates to a second one —
					// deleting a service account immediately breaks any
					// workload authenticating as it.
					s.pendingAction = "delete-confirm2"
					return s, nil
				}
				var actionCmd tea.Cmd
				if s.selectedAccount != nil {
					switch s.pendingAction {
					case "delete-confirm2":
						actionCmd = s.deleteAccountCmd(*s.selectedAccount)
					case "enable":
						actionCmd = s.enableAccountCmd(*s.selectedAccount)
					case "disable":
						actionCmd = s.disableAccountCmd(*s.selectedAccount)
					case "grant":
						actionCmd = s.addIAMBindingCmd(*s.selectedAccount, s.pendingIAMRole, s.pendingIAMMember)
					case "delete-key":
						if s.selectedKey != nil {
							actionCmd = s.deleteKeyCmd(*s.selectedKey)
						}
					}
				}
				s.viewConfirm = false
				return s, actionCmd
			case "n", "esc", "q":
				s.viewConfirm = false
				s.pendingAction = ""
				if s.selectedKey != nil {
					s.viewKeys = true
				} else {
					s.viewIAM = s.iamBindings != nil
				}
				return s, nil
			}
			return s, nil
		}

		if s.viewKeys {
			switch msg.String() {
			case "esc", "q":
				s.viewKeys = false
				s.keys = nil
				s.selectedKey = nil
				return s, nil
			case "n":
				s.keyCreateForm = components.NewForm("Create Service Account Key", []components.FormField{
					{Label: "Output File Path", Placeholder: "/path/to/key.json", Required: true},
				})
				s.viewKeys = false
				s.viewKeyCreate = true
				return s, nil
			case "d":
				if idx := s.keyTable.Cursor(); idx >= 0 && idx < len(s.keys) {
					s.selectedKey = &s.keys[idx]
					s.pendingAction = "delete-key"
					s.viewKeys = false
					s.viewConfirm = true
				}
				return s, nil
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.keyTable.Update(msg)
			s.keyTable = updatedTable
			return s, cmd
		}

		if s.viewIAM {
			switch msg.String() {
			case "esc", "q":
				s.viewIAM = false
				s.iamBindings = nil
				return s, nil
			case "a":
				if s.selectedAccount != nil {
					s.iamForm = components.NewIAMAddBindingForm(s.selectedAccount.Email)
					s.viewIAM = false
					s.viewIAMForm = true
				}
				return s, nil
			}
			return s, nil
		}

		if s.viewDetail {
			// Detail View Keybindings
			switch msg.String() {
			case "esc", "q":
				s.viewDetail = false
				s.selectedAccount = nil
				return s, nil
			case "u":
				if s.selectedAccount != nil {
					s.updateForm = newAccountUpdateForm(*s.selectedAccount)
					s.viewUpdate = true
				}
				return s, nil
			case "E": // Enable (Confirm)
				if s.selectedAccount != nil {
					s.pendingAction = "enable"
					s.viewConfirm = true
				}
				return s, nil
			case "D": // Disable (Confirm)
				if s.selectedAccount != nil {
					s.pendingAction = "disable"
					s.viewConfirm = true
				}
				return s, nil
			case "d":
				if s.selectedAccount != nil {
					s.pendingAction = "delete"
					s.viewConfirm = true
				}
				return s, nil
			case "i": // View/grant IAM policy on this service account
				if s.selectedAccount != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchIAMPolicyCmd(*s.selectedAccount))
				}
				return s, nil
			case "K": // View/manage keys
				if s.selectedAccount != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchKeysCmd(*s.selectedAccount))
				}
				return s, nil
			}
		} else {
			// List View Keybindings
			switch msg.String() {
			case "r":
				return s, s.fetchAccountsCmd(true)
			case "n":
				s.createForm = components.NewForm("New Service Account", []components.FormField{
					{Label: "Account ID", Placeholder: "my-service-account", Required: true},
					{Label: "Display Name", Placeholder: "My Service Account"},
				})
				s.viewCreate = true
				return s, nil
			case "U": // Undelete a recently-deleted service account by unique ID
				s.undeleteForm = components.NewForm("Undelete Service Account", []components.FormField{
					{Label: "Unique ID", Placeholder: "112233445566778899", Required: true},
				})
				s.viewUndelete = true
				return s, nil
			case "enter":
				if idx := s.table.Cursor(); idx >= 0 && idx < len(s.accounts) {
					s.selectedAccount = &s.accounts[idx]
					s.viewDetail = true
				}
			}
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.table.Update(msg)
			s.table = updatedTable
			return s, cmd
		}
	}

	return s, nil
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Service Accounts")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewCreate {
		return s.createForm.View()
	}

	if s.viewUpdate {
		return s.updateForm.View()
	}

	if s.viewConfirm {
		return s.renderConfirmation()
	}

	if s.viewIAMForm {
		return s.iamForm.View()
	}

	if s.viewUndelete {
		return s.undeleteForm.View()
	}

	if s.viewKeyCreate {
		return s.keyCreateForm.View()
	}

	if s.viewKeys {
		return s.renderKeysView()
	}

	if s.viewIAM {
		return s.renderIAMView()
	}

	if s.viewDetail {
		return s.renderDetailView()
	}

	return s.renderServiceAccountsList()
}

// renderIAMView renders the selected service account's own IAM policy --
// who can act as/impersonate this identity.
func (s *Service) renderIAMView() string {
	if s.selectedAccount == nil {
		return "Error: No service account selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		s.selectedAccount.Email,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedAccount.Email, rows)
}

// renderKeysView renders the selected service account's user-managed keys.
func (s *Service) renderKeysView() string {
	if s.selectedAccount == nil {
		return "Error: No service account selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		s.selectedAccount.Email,
		"Keys",
	)
	if len(s.keys) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("keys"))
	}
	hint := styles.HelpStyle.Render("n Create Key  |  d Delete Key  |  q Back")
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.keyTable.View(), "", hint)
}

// renderConfirmation renders the account-delete confirmation dialog. Service
// account delete requires a second confirmation ("delete-confirm2") because
// it immediately breaks any workload authenticating as this identity.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "grant" {
		if s.selectedAccount == nil {
			return "Error: No service account selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedAccount.Email,
			"service account",
			components.IAMConfirmMessage("service account", s.selectedAccount.Email, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	if s.pendingAction == "delete-key" {
		if s.selectedKey == nil {
			return "Error: No key selected"
		}
		return components.RenderConfirmation("delete", s.selectedKey.KeyID, "service account key")
	}
	if s.selectedAccount == nil {
		return "Error: No service account selected"
	}
	if s.pendingAction == "delete-confirm2" {
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedAccount.Email,
			"service account",
			fmt.Sprintf("FINAL WARNING: this immediately breaks any workload authenticating as %s.", s.selectedAccount.Email),
		)
	}
	if s.pendingAction == "delete" {
		return components.RenderConfirmation("delete", s.selectedAccount.Email, "service account")
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedAccount.Email, "service account")
}

// Cmd to fetch accounts
func (s *Service) fetchAccountsCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := "iam_accounts"

		// 1. Check Cache
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if accs, ok := val.([]ServiceAccount); ok {
					return accountsMsg(accs)
				}
			}
		}

		// 2. API Call
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		accs, err := s.client.ListServiceAccounts(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		// 3. Update Cache
		if s.cache != nil {
			s.cache.Set(key, accs, CacheTTL)
		}

		return accountsMsg(accs)
	}
}

// fetchPolicyCmd fetches the project's IAM policy bindings. Failures are
// swallowed (no s.err) since this is supplementary to the service-account
// list — a missing resourcemanager.projects.getIamPolicy permission
// shouldn't block the rest of the page.
func (s *Service) fetchPolicyCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("iam_policy:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if bindings, ok := val.([]PolicyMember); ok {
					return policyBindingsMsg(bindings)
				}
			}
		}

		if s.client == nil {
			return policyBindingsMsg(nil)
		}
		bindings, err := s.client.GetProjectPolicyBindings(s.projectID)
		if err != nil {
			return policyBindingsMsg(nil)
		}

		if s.cache != nil {
			s.cache.Set(key, bindings, CacheTTL)
		}

		return policyBindingsMsg(bindings)
	}
}

func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchAccountsCmd(false),
		s.fetchPolicyCmd(false),
	)
}

func (s *Service) Reset() {
	s.viewDetail = false
	s.viewCreate = false
	s.viewUpdate = false
	s.viewConfirm = false
	s.viewIAM = false
	s.viewIAMForm = false
	s.viewKeys = false
	s.viewKeyCreate = false
	s.viewUndelete = false
	s.pendingAction = ""
	s.selectedAccount = nil
	s.selectedKey = nil
	s.iamBindings = nil
	s.keys = nil
	s.err = nil // Fix: Clear previous errors on reset
	s.table.SetCursor(0)
}

func (s *Service) IsRootView() bool {
	return !s.viewDetail && !s.viewCreate && !s.viewUpdate && !s.viewConfirm &&
		!s.viewIAM && !s.viewIAMForm && !s.viewKeys && !s.viewKeyCreate && !s.viewUndelete
}

// submitCreateCmd fires the CreateServiceAccount API call using the
// current create-form values.
func (s *Service) submitCreateCmd() tea.Cmd {
	accountID := s.createForm.Value("Account ID")
	displayName := s.createForm.Value("Display Name")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateServiceAccount(s.projectID, accountID, displayName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Service account %s created", accountID)}
	}
}

// submitUpdateCmd fires the UpdateServiceAccountDisplayName API call using
// the current update-form value.
func (s *Service) submitUpdateCmd(acc ServiceAccount) tea.Cmd {
	displayName := s.updateForm.Value("Display Name")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateServiceAccountDisplayName(acc.Name, displayName); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updating service account %s...", acc.Email)}
	}
}

// deleteAccountCmd triggers deletion of the given service account
func (s *Service) deleteAccountCmd(acc ServiceAccount) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteServiceAccount(acc.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting service account %s...", acc.Email)}
	}
}

// disableAccountCmd triggers disabling the given service account
func (s *Service) disableAccountCmd(acc ServiceAccount) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DisableServiceAccount(acc.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Disabling service account %s...", acc.Email)}
	}
}

// enableAccountCmd triggers re-enabling the given service account
func (s *Service) enableAccountCmd(acc ServiceAccount) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.EnableServiceAccount(acc.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Enabling service account %s...", acc.Email)}
	}
}

// undeleteAccountCmd restores a recently-deleted service account by its
// unique ID.
func (s *Service) undeleteAccountCmd(uniqueID string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UndeleteServiceAccount(s.projectID, uniqueID); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Undeleted service account %s", uniqueID)}
	}
}

// fetchIAMPolicyCmd fetches the given service account's own IAM policy.
func (s *Service) fetchIAMPolicyCmd(acc ServiceAccount) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetServiceAccountIAMPolicy(acc.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given service account's own
// IAM policy (who can act as/impersonate it).
func (s *Service) addIAMBindingCmd(acc ServiceAccount, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddServiceAccountIAMBinding(acc.Name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on service account %s", role, member, acc.Email)}
	}
}

// fetchKeysCmd fetches the given service account's user-managed keys.
func (s *Service) fetchKeysCmd(acc ServiceAccount) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return keysMsg{err: fmt.Errorf("client not initialized")}
		}
		keys, err := s.client.ListServiceAccountKeys(acc.Name)
		if err != nil {
			return keysMsg{err: err}
		}
		return keysMsg{keys: keys}
	}
}

// createKeyCmd creates a new user-managed key for acc, writing the private
// key JSON to outputPath.
func (s *Service) createKeyCmd(acc ServiceAccount, outputPath string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateServiceAccountKey(acc.Name, outputPath); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Created key for %s, written to %s", acc.Email, outputPath)}
	}
}

// deleteKeyCmd deletes a user-managed key.
func (s *Service) deleteKeyCmd(key ServiceAccountKey) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteServiceAccountKey(key.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleted key %s", key.KeyID)}
	}
}

// Internal Helpers

func (s *Service) updateTable(accounts []ServiceAccount) {
	rows := make([]table.Row, len(accounts))
	for i, acc := range accounts {
		status := "Active"
		if acc.Disabled {
			status = "Disabled"
		}

		rows[i] = table.Row{
			acc.DisplayName,
			acc.Email,
			status,
			acc.UniqueID,
		}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateKeyTable(keys []ServiceAccountKey) {
	rows := make([]table.Row, len(keys))
	for i, k := range keys {
		rows[i] = table.Row{
			k.KeyID,
			k.ValidAfterTime,
			k.ValidBeforeTime,
			fmt.Sprintf("%t", k.Disabled),
		}
	}
	s.keyTable.SetRows(rows)
}
