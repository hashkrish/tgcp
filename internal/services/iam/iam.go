package iam

import (
	"context"

	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
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
	selectedAccount *ServiceAccount

	// pendingAction is "delete" then escalates to "delete-confirm2" — service
	// account delete immediately breaks any workload using that identity, so
	// this requires a double confirmation.
	pendingAction string

	createForm components.FormModel
	updateForm components.FormModel

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

	return &Service{
		table:   t,
		spinner: components.NewSpinner(),
		cache:   cache,
	}
}

func (s *Service) Name() string {
	return "Identity & Access Management"
}

func (s *Service) ShortName() string {
	return "iam"
}

func (s *Service) HelpText() string {
	if s.viewCreate || s.viewUpdate {
		return "Tab/↑↓ Move  Enter/Ctrl+S Submit  Esc Cancel"
	}
	if s.viewConfirm {
		return "y:Confirm  n:Cancel"
	}
	if s.viewDetail {
		return "Esc/q:Back  u:Update  E:Enable  D:Disable  d:Delete"
	}
	return "r:Refresh  n:New Service Account  Ent:Detail"
}

// newAccountUpdateForm builds the FormModel for updating a service
// account's display name, seeded with its current value. Description and
// the disable/enable/undelete/keys operations are out of scope.
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

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if s.pendingAction != "" {
			action := s.pendingAction
			s.pendingAction = ""
			if msg.err != nil {
				return s, func() tea.Msg {
					return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError}
				}
			}
			// Only a completed delete drops back to the list — a deleted
			// account no longer exists. Disable/enable leave the detail
			// view up so the refreshed status is visible in place.
			if action == "delete-confirm2" {
				s.viewDetail = false
				s.selectedAccount = nil
			}
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if msg.err != nil {
			if s.viewUpdate {
				s.updateForm.SubmitErr = msg.err.Error()
			} else {
				s.createForm.SubmitErr = msg.err.Error()
			}
			return s, nil
		}
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
		if !s.viewDetail && !s.viewCreate && !s.viewUpdate && !s.viewConfirm {
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
					}
				}
				s.viewConfirm = false
				return s, actionCmd
			case "n", "esc", "q":
				s.viewConfirm = false
				s.pendingAction = ""
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

	if s.viewDetail {
		return s.renderDetailView()
	}

	return s.renderServiceAccountsList()
}

// renderConfirmation renders the account-delete confirmation dialog. Service
// account delete requires a second confirmation ("delete-confirm2") because
// it immediately breaks any workload authenticating as this identity.
func (s *Service) renderConfirmation() string {
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
	s.pendingAction = ""
	s.selectedAccount = nil
	s.err = nil // Fix: Clear previous errors on reset
	s.table.SetCursor(0)
}

func (s *Service) IsRootView() bool {
	return !s.viewDetail && !s.viewCreate && !s.viewUpdate && !s.viewConfirm
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
