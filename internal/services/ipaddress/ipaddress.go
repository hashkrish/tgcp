package ipaddress

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

const CacheTTL = 30 * time.Second

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewList ViewState = iota
	ViewDetail
	ViewConfirmation
	ViewCreate
)

// newAddressCreateForm builds the FormModel for reserving a new static IP
// address. Leaving Region blank reserves a global address (used by global
// external load balancers/Cloud CDN); anything else reserves a regional one.
func newAddressCreateForm() components.FormModel {
	return components.NewForm("Reserve IP Address", []components.FormField{
		{Label: "Name", Placeholder: "my-static-ip", Required: true},
		{Label: "Region", Placeholder: "us-central1 (blank = global)"},
		{Label: "Type", Default: "EXTERNAL", Placeholder: "EXTERNAL or INTERNAL", Required: true, Validate: func(v string) string {
			switch strings.ToUpper(v) {
			case "EXTERNAL", "INTERNAL":
				return ""
			default:
				return "must be EXTERNAL or INTERNAL"
			}
		}},
	})
}

type addressesMsg []Address
type errMsg error

// actionResultMsg carries the result of an async address action (reserve,
// release). action/name/region are captured at Cmd-creation time (rather
// than read back off s.pendingAction, which is already reset by the time
// this message arrives) so callers can label toasts and other handling
// correctly.
type actionResultMsg struct {
	err    error
	msg    string
	action string
	name   string
	region string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string
	table     *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Address]

	addresses []Address
	spinner   components.SpinnerModel
	err       error

	viewState       ViewState
	selectedAddress *Address

	// Confirmation State
	pendingAction string    // "delete"
	actionSource  ViewState // Where to return after confirmation

	// Create State
	createForm components.FormModel

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Address", Width: 18},
		{Title: "Scope", Width: 15},
		{Title: "Type", Width: 10},
		{Title: "Status", Width: 10},
	}

	t := components.NewStandardTable(columns)

	svc := &Service{
		table:     t,
		filter:    components.NewFilterWithPlaceholder("Filter addresses..."),
		spinner:   components.NewSpinner(),
		viewState: ViewList,
		cache:     cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredAddresses, svc.updateTable)
	return svc
}

func (s *Service) Name() string {
	return "IP Addresses"
}

func (s *Service) ShortName() string {
	return "ipaddress"
}

func (s *Service) HelpText() string {
	if s.viewState == ViewList {
		return "r:Refresh  /:Filter  Ent:Detail  n:Reserve  d:Release"
	}
	if s.viewState == ViewDetail {
		return "Esc/q:Back  d:Release"
	}
	if s.viewState == ViewConfirmation {
		return "y:Confirm  n:Cancel"
	}
	if s.viewState == ViewCreate {
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
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
		s.fetchAddressesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewList
	s.selectedAddress = nil
	s.pendingAction = ""
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
		return s, tea.Batch(s.fetchAddressesCmd(false), s.tick())

	case addressesMsg:
		s.spinner.Stop()
		s.addresses = msg
		s.filterSession.Apply(s.addresses)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
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
			case "enter":
				addrs := s.getFilteredAddresses(s.addresses, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(addrs) {
					s.selectedAddress = &addrs[idx]
					s.viewState = ViewDetail
				}
			case "n": // Reserve
				s.createForm = newAddressCreateForm()
				s.viewState = ViewCreate
				return s, nil
			case "d": // Release (Confirm)
				addrs := s.getFilteredAddresses(s.addresses, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(addrs) {
					s.selectedAddress = &addrs[idx]
					s.pendingAction = "delete"
					s.actionSource = ViewList
					s.viewState = ViewConfirmation
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
				s.selectedAddress = nil
				return s, nil
			case "d": // Release (Confirm)
				if s.selectedAddress != nil {
					s.pendingAction = "delete"
					s.actionSource = ViewDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			}
		}

		if s.viewState == ViewConfirmation {
			switch msg.String() {
			case "y", "enter": // Confirm
				var actionCmd tea.Cmd
				if s.pendingAction == "delete" && s.selectedAddress != nil {
					actionCmd = s.DeleteAddressCmd(*s.selectedAddress)
				}
				s.viewState = ViewList
				s.selectedAddress = nil
				s.pendingAction = ""
				return s, actionCmd
			case "n", "esc", "q": // Cancel
				s.viewState = s.actionSource
				s.pendingAction = ""
				return s, nil
			}
		}

		if s.viewState == ViewCreate {
			result, fcmd := s.createForm.Update(msg)
			if result.Cancelled {
				s.viewState = ViewList
				return s, nil
			}
			if result.Submitted {
				vals := s.createForm.Values()
				s.viewState = ViewList
				return s, s.CreateAddressCmd(vals["Name"], vals["Region"], strings.ToUpper(vals["Type"]))
			}
			return s, fcmd
		}
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// CreateAddressCmd triggers reservation of a new static IP address.
func (s *Service) CreateAddressCmd(name, region, addressType string) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "address", Name: name, Action: "create",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.CreateAddress(s.projectID, region, name, addressType)
		})
		if err != nil {
			return actionResultMsg{err: err, action: "create", name: name, region: region}
		}
		return actionResultMsg{msg: fmt.Sprintf("Reserving address %s...", name), action: "create", name: name, region: region}
	}
}

// DeleteAddressCmd triggers release of the given address.
func (s *Service) DeleteAddressCmd(addr Address) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "address", Name: addr.Name, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteAddress(s.projectID, addr.Region, addr.Name)
		})
		if err != nil {
			return actionResultMsg{err: err, action: "delete", name: addr.Name, region: addr.Region}
		}
		return actionResultMsg{msg: fmt.Sprintf("Releasing address %s...", addr.Name), action: "delete", name: addr.Name, region: addr.Region}
	}
}

func (s *Service) fetchAddressesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("ipaddresses:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Address); ok {
					return addressesMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListAddresses(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return addressesMsg(items)
	}
}

func (s *Service) updateTable(items []Address) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Address,
			item.Scope(),
			item.AddressType,
			item.Status,
		}
	}
	s.table.SetRows(rows)
}

// getFilteredAddresses returns filtered addresses based on the query string
func (s *Service) getFilteredAddresses(addresses []Address, query string) []Address {
	if query == "" {
		return addresses
	}
	return components.FilterSlice(addresses, query, func(a Address, q string) bool {
		return components.ContainsMatch(a.Name, a.Address, a.Scope(), a.AddressType, a.Status)(q)
	})
}
