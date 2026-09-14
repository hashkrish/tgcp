package dns

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

const CacheTTL = 60 * time.Second

// -----------------------------------------------------------------------------
// Models & Msgs
// -----------------------------------------------------------------------------

type tickMsg time.Time

type ViewState int

const (
	ViewZones ViewState = iota
	ViewRecords
	ViewCreate
	ViewConfirmation
	ViewUpdateZone
	ViewIAM
	ViewIAMForm
	ViewUpdateRecord
	ViewCreateRecord
)

type zonesMsg []Zone
type recordsMsg []RecordSet
type errMsg error

// actionResultMsg carries the result of an async create action.
type actionResultMsg struct {
	err error
	msg string
}

// iamPolicyMsg carries the result of a GetZoneIAMPolicy fetch.
type iamPolicyMsg struct {
	bindings []IAMBinding
	err      error
}

// newZoneUpdateForm builds the FormModel for updating a zone's
// description, seeded with its current value. DNSSEC config and other zone
// fields are out of scope for this minimal Update flow.
func newZoneUpdateForm(z Zone) components.FormModel {
	return components.NewForm("Update Zone: "+z.Name, []components.FormField{
		{Label: "Description", Default: z.Description},
	})
}

// newRecordUpdateForm builds the FormModel for updating an existing record
// set's TTL and data, seeded with its current values.
func newRecordUpdateForm(r RecordSet) components.FormModel {
	return components.NewForm("Update Record: "+r.Name+" ("+r.Type+")", []components.FormField{
		{Label: "TTL", Default: fmt.Sprintf("%d", r.TTL), Required: true},
		{Label: "Data (comma-separated)", Default: strings.Join(r.Rrdatas, ","), Required: true},
	})
}

// newRecordCreateForm builds the FormModel for creating a new record set in
// the given zone.
func newRecordCreateForm(z Zone) components.FormModel {
	return components.NewForm("New Record in "+z.Name, []components.FormField{
		{Label: "Name", Placeholder: z.DNSName, Required: true},
		{Label: "Type", Placeholder: "A", Default: "A", Required: true},
		{Label: "TTL", Default: "300", Required: true},
		{Label: "Data (comma-separated)", Required: true},
	})
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

type Service struct {
	client    *Client
	projectID string

	zoneTable   *components.StandardTable
	recordTable *components.StandardTable

	filter        components.FilterModel
	filterSession components.FilterSession[Zone]

	zones   []Zone
	records []RecordSet
	spinner components.SpinnerModel
	err     error

	viewState      ViewState
	selectedZone   *Zone
	selectedRecord *RecordSet

	createForm       components.FormModel
	updateZoneForm   components.FormModel
	updateRecordForm components.FormModel
	createRecordForm components.FormModel
	iamForm          components.FormModel

	// IAM State: current bindings for the selected zone, and the
	// add-binding form.
	iamBindings      []IAMBinding
	pendingIAMRole   string
	pendingIAMMember string

	// Confirmation State
	pendingAction string    // "delete", "grant"
	actionSource  ViewState // Where to return after confirmation

	cache *core.Cache
}

func NewService(cache *core.Cache) *Service {
	zoneColumns := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "DNS Name", Width: 30},
		{Title: "Visibility", Width: 12},
		{Title: "Description", Width: 30},
	}
	zt := components.NewStandardTable(zoneColumns)

	recordColumns := []table.Column{
		{Title: "Name", Width: 35},
		{Title: "Type", Width: 8},
		{Title: "TTL", Width: 8},
		{Title: "Data", Width: 40},
	}
	rt := components.NewStandardTable(recordColumns)

	svc := &Service{
		zoneTable:   zt,
		recordTable: rt,
		filter:      components.NewFilterWithPlaceholder("Filter zones..."),
		spinner:     components.NewSpinner(),
		viewState:   ViewZones,
		cache:       cache,
	}
	svc.filterSession = components.NewFilterSession(&svc.filter, svc.getFilteredZones, svc.updateZoneTable)
	return svc
}

func (s *Service) Name() string {
	return "Cloud DNS"
}

func (s *Service) ShortName() string {
	return "dns"
}

func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewZones:
		return "r:Refresh  /:Filter  n:New Zone  Ent:Records  u:Update  i:IAM  d:Delete"
	case ViewRecords:
		return "Esc/q:Back  n:New Record  u:Update Record  d:Delete Record"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	case ViewIAM:
		return "a:Add Binding  q/Esc:Back"
	case ViewCreate, ViewUpdateZone, ViewIAMForm, ViewUpdateRecord, ViewCreateRecord:
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
	return tea.Batch(s.spinner.Start(""), s.fetchZonesCmd(false), s.tick())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (s *Service) Refresh() tea.Cmd {
	if s.viewState == ViewRecords && s.selectedZone != nil {
		return tea.Batch(
			s.spinner.Start(""),
			s.fetchRecordsCmd(s.selectedZone.Name),
		)
	}
	return tea.Batch(
		s.spinner.Start(""),
		s.fetchZonesCmd(true),
	)
}

func (s *Service) Reset() {
	s.viewState = ViewZones
	s.selectedZone = nil
	s.selectedRecord = nil
	s.records = nil
	s.iamBindings = nil
	s.pendingAction = ""
	s.err = nil
	s.zoneTable.SetCursor(0)
	s.recordTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

func (s *Service) IsRootView() bool {
	return s.viewState == ViewZones
}

func (s *Service) Focus() {
	s.zoneTable.Focus()
}

func (s *Service) Blur() {
	s.zoneTable.Blur()
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
		if s.viewState == ViewZones {
			return s, tea.Batch(s.fetchZonesCmd(false), s.tick())
		}
		return s, s.tick()

	case zonesMsg:
		s.spinner.Stop()
		s.zones = msg
		s.filterSession.Apply(s.zones)
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case recordsMsg:
		s.spinner.Stop()
		s.records = msg
		s.updateRecordTable(msg)
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
		if s.pendingAction == "delete" {
			s.pendingAction = ""
			name := ""
			if s.selectedZone != nil {
				name = s.selectedZone.Name
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "managed zone",
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
				Resource:  "managed zone",
				Name:      name,
				Action:    "delete",
				Status:    core.JobSuccess,
			})
			s.selectedZone = nil
			s.viewState = ViewZones
			return s, tea.Batch(
				func() tea.Msg {
					return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess}
				},
				s.Refresh(),
			)
		}
		if s.pendingAction == "delete-record" {
			s.pendingAction = ""
			name := ""
			if s.selectedRecord != nil {
				name = s.selectedRecord.Name
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "record",
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
				Resource:  "record",
				Name:      name,
				Action:    "delete",
				Status:    core.JobSuccess,
			})
			s.selectedRecord = nil
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			if s.selectedZone != nil {
				return s, tea.Batch(toast, s.fetchRecordsCmd(s.selectedZone.Name))
			}
			return s, toast
		}
		if s.pendingAction == "grant" {
			s.pendingAction = ""
			name := ""
			if s.selectedZone != nil {
				name = s.selectedZone.Name
			}
			if msg.err != nil {
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "managed zone",
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
				Resource:  "managed zone",
				Name:      name,
				Action:    "grant",
				Status:    core.JobSuccess,
			})
			if s.selectedZone != nil {
				return s, tea.Batch(
					func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
					s.fetchIAMCmd(*s.selectedZone),
				)
			}
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
		}
		if s.viewState == ViewUpdateZone {
			name := ""
			if s.selectedZone != nil {
				name = s.selectedZone.Name
			}
			if msg.err != nil {
				s.updateZoneForm.SubmitErr = msg.err.Error()
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "managed zone",
					Name:      name,
					Action:    "update",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, nil
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "managed zone",
				Name:      name,
				Action:    "update",
				Status:    core.JobSuccess,
			})
			s.viewState = ViewZones
			return s, tea.Batch(
				func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} },
				s.Refresh(),
			)
		}
		if s.viewState == ViewUpdateRecord {
			name := ""
			if s.selectedRecord != nil {
				name = s.selectedRecord.Name
			}
			if msg.err != nil {
				s.updateRecordForm.SubmitErr = msg.err.Error()
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "record",
					Name:      name,
					Action:    "update",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, nil
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "record",
				Name:      name,
				Action:    "update",
				Status:    core.JobSuccess,
			})
			s.viewState = ViewRecords
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			if s.selectedZone != nil {
				return s, tea.Batch(toast, s.fetchRecordsCmd(s.selectedZone.Name))
			}
			return s, toast
		}
		if s.viewState == ViewCreateRecord {
			name := s.createRecordForm.Value("Name")
			if msg.err != nil {
				s.createRecordForm.SubmitErr = msg.err.Error()
				core.RecordJob(core.Job{
					Service:   s.ShortName(),
					ProjectID: s.projectID,
					Resource:  "record",
					Name:      name,
					Action:    "create",
					Status:    core.JobFailed,
					Error:     msg.err.Error(),
				})
				return s, nil
			}
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "record",
				Name:      name,
				Action:    "create",
				Status:    core.JobSuccess,
			})
			s.viewState = ViewRecords
			toast := func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} }
			if s.selectedZone != nil {
				return s, tea.Batch(toast, s.fetchRecordsCmd(s.selectedZone.Name))
			}
			return s, toast
		}
		if msg.err != nil {
			s.createForm.SubmitErr = msg.err.Error()
			core.RecordJob(core.Job{
				Service:   s.ShortName(),
				ProjectID: s.projectID,
				Resource:  "managed zone",
				Name:      s.createForm.Value("Zone Name"),
				Action:    "create",
				Status:    core.JobFailed,
				Error:     msg.err.Error(),
			})
			return s, nil
		}
		core.RecordJob(core.Job{
			Service:   s.ShortName(),
			ProjectID: s.projectID,
			Resource:  "managed zone",
			Name:      s.createForm.Value("Zone Name"),
			Action:    "create",
			Status:    core.JobSuccess,
		})
		s.viewState = ViewZones
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
		s.zoneTable.HandleWindowSizeDefault(msg)
		s.recordTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		switch s.viewState {
		case ViewZones:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.zoneTable.Update(msg)
			s.zoneTable = updatedTable
			return s, cmd
		case ViewRecords:
			var updatedTable *components.StandardTable
			updatedTable, cmd = s.recordTable.Update(msg)
			s.recordTable = updatedTable
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
			s.viewState = ViewZones
			return s, nil
		}
		if result.Submitted {
			return s, s.submitCreateCmd()
		}
		return s, formCmd
	}

	if s.viewState == ViewZones {
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
			s.createForm = components.NewForm("New Managed Zone", []components.FormField{
				{Label: "Zone Name", Placeholder: "my-zone", Required: true},
				{Label: "DNS Name", Placeholder: "example.com.", Required: true},
				{Label: "Description"},
				{Label: "Visibility", Default: "public"},
			})
			s.viewState = ViewCreate
			return s, nil
		case "enter":
			zones := s.getFilteredZones(s.zones, s.filter.Value())
			if idx := s.zoneTable.Cursor(); idx >= 0 && idx < len(zones) {
				s.selectedZone = &zones[idx]
				s.viewState = ViewRecords
				return s, tea.Batch(
					s.spinner.Start(""),
					s.fetchRecordsCmd(s.selectedZone.Name),
				)
			}
		case "d":
			zones := s.getFilteredZones(s.zones, s.filter.Value())
			if idx := s.zoneTable.Cursor(); idx >= 0 && idx < len(zones) {
				s.selectedZone = &zones[idx]
				s.pendingAction = "delete"
				s.actionSource = ViewZones
				s.viewState = ViewConfirmation
				return s, nil
			}
		case "u":
			zones := s.getFilteredZones(s.zones, s.filter.Value())
			if idx := s.zoneTable.Cursor(); idx >= 0 && idx < len(zones) {
				s.selectedZone = &zones[idx]
				s.updateZoneForm = newZoneUpdateForm(*s.selectedZone)
				s.viewState = ViewUpdateZone
				return s, nil
			}
		case "i":
			zones := s.getFilteredZones(s.zones, s.filter.Value())
			if idx := s.zoneTable.Cursor(); idx >= 0 && idx < len(zones) {
				s.selectedZone = &zones[idx]
				return s, tea.Batch(s.fetchIAMCmd(*s.selectedZone), s.spinner.Start(""))
			}
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.zoneTable.Update(msg)
		s.zoneTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewUpdateZone {
		result, formCmd := s.updateZoneForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewZones
			return s, nil
		}
		if result.Submitted && s.selectedZone != nil {
			return s, s.updateZoneCmd(*s.selectedZone, s.updateZoneForm.Value("Description"))
		}
		return s, formCmd
	}

	if s.viewState == ViewIAM {
		switch msg.String() {
		case "q", "esc":
			s.viewState = ViewZones
			return s, nil
		case "a":
			if s.selectedZone != nil {
				s.iamForm = components.NewIAMAddBindingForm(s.selectedZone.Name)
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
		if result.Submitted && s.selectedZone != nil {
			s.pendingIAMRole = s.iamForm.Value("Role")
			s.pendingIAMMember = s.iamForm.Value("Member")
			s.pendingAction = "grant"
			s.actionSource = ViewIAM
			s.viewState = ViewConfirmation
			return s, nil
		}
		return s, formCmd
	}

	if s.viewState == ViewConfirmation {
		switch msg.String() {
		case "y", "enter":
			var actionCmd tea.Cmd
			if s.pendingAction == "delete" && s.selectedZone != nil {
				actionCmd = s.deleteZoneCmd(*s.selectedZone)
			} else if s.pendingAction == "delete-record" && s.selectedZone != nil && s.selectedRecord != nil {
				actionCmd = s.deleteRecordCmd(*s.selectedZone, *s.selectedRecord)
			} else if s.pendingAction == "grant" && s.selectedZone != nil {
				actionCmd = s.addIAMBindingCmd(*s.selectedZone, s.pendingIAMRole, s.pendingIAMMember)
			}
			s.viewState = s.actionSource
			return s, actionCmd
		case "n", "esc", "q":
			s.viewState = s.actionSource
			s.pendingAction = ""
			return s, nil
		}
	}

	if s.viewState == ViewRecords {
		switch msg.String() {
		case "r":
			return s, s.Refresh()
		case "esc", "q":
			s.viewState = ViewZones
			s.selectedZone = nil
			s.records = nil
			return s, nil
		case "u":
			if idx := s.recordTable.Cursor(); idx >= 0 && idx < len(s.records) {
				s.selectedRecord = &s.records[idx]
				s.updateRecordForm = newRecordUpdateForm(*s.selectedRecord)
				s.viewState = ViewUpdateRecord
				return s, nil
			}
		case "n":
			if s.selectedZone != nil {
				s.createRecordForm = newRecordCreateForm(*s.selectedZone)
				s.viewState = ViewCreateRecord
				return s, nil
			}
		case "d":
			if idx := s.recordTable.Cursor(); idx >= 0 && idx < len(s.records) {
				s.selectedRecord = &s.records[idx]
				s.pendingAction = "delete-record"
				s.actionSource = ViewRecords
				s.viewState = ViewConfirmation
				return s, nil
			}
		}

		var updatedTable *components.StandardTable
		updatedTable, cmd = s.recordTable.Update(msg)
		s.recordTable = updatedTable
		return s, cmd
	}

	if s.viewState == ViewUpdateRecord {
		result, formCmd := s.updateRecordForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewRecords
			return s, nil
		}
		if result.Submitted && s.selectedZone != nil && s.selectedRecord != nil {
			ttl, err := strconv.ParseInt(s.updateRecordForm.Value("TTL"), 10, 64)
			if err != nil {
				s.updateRecordForm.SubmitErr = "TTL must be a whole number of seconds"
				return s, nil
			}
			rrdatas := strings.Split(s.updateRecordForm.Value("Data (comma-separated)"), ",")
			for i := range rrdatas {
				rrdatas[i] = strings.TrimSpace(rrdatas[i])
			}
			return s, s.updateRecordCmd(*s.selectedZone, *s.selectedRecord, ttl, rrdatas)
		}
		return s, formCmd
	}

	if s.viewState == ViewCreateRecord {
		result, formCmd := s.createRecordForm.Update(msg)
		if result.Cancelled {
			s.viewState = ViewRecords
			return s, nil
		}
		if result.Submitted && s.selectedZone != nil {
			ttl, err := strconv.ParseInt(s.createRecordForm.Value("TTL"), 10, 64)
			if err != nil {
				s.createRecordForm.SubmitErr = "TTL must be a whole number of seconds"
				return s, nil
			}
			rrdatas := strings.Split(s.createRecordForm.Value("Data (comma-separated)"), ",")
			for i := range rrdatas {
				rrdatas[i] = strings.TrimSpace(rrdatas[i])
			}
			return s, s.createRecordCmd(*s.selectedZone, s.createRecordForm.Value("Name"), s.createRecordForm.Value("Type"), ttl, rrdatas)
		}
		return s, formCmd
	}

	return s, nil
}

// -----------------------------------------------------------------------------
// Views
// -----------------------------------------------------------------------------

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, s.Name(), "Zones")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewRecords {
		return s.renderRecordsView()
	}

	if s.viewState == ViewCreate {
		return s.createForm.View()
	}

	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}

	if s.viewState == ViewUpdateZone {
		return s.updateZoneForm.View()
	}

	if s.viewState == ViewUpdateRecord {
		return s.updateRecordForm.View()
	}

	if s.viewState == ViewCreateRecord {
		return s.createRecordForm.View()
	}

	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}

	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}

	return s.renderListView()
}

// renderIAMView renders the current IAM policy bindings for the selected
// zone, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedZone == nil {
		return "Error: No zone selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Zones",
		s.selectedZone.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedZone.Name, rows)
}

// renderConfirmation renders the zone-delete or record-delete confirmation
// dialog, depending on s.pendingAction.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "delete-record" {
		if s.selectedRecord == nil {
			return "Error: No record selected"
		}
		return components.RenderConfirmationWithMessage(
			"delete",
			s.selectedRecord.Name,
			"record",
			fmt.Sprintf("Are you sure you want to DELETE record %s (%s)? The zone's apex NS/SOA records cannot be deleted.", s.selectedRecord.Name, s.selectedRecord.Type),
		)
	}
	if s.selectedZone == nil {
		return "Error: No zone selected"
	}
	return components.RenderConfirmationWithMessage(
		s.pendingAction,
		s.selectedZone.Name,
		"managed zone",
		fmt.Sprintf("Are you sure you want to DELETE zone %s? Only zones with no records beyond the default NS/SOA can be deleted.", s.selectedZone.Name),
	)
}

func (s *Service) renderListView() string {
	var content strings.Builder
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Zones",
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	content.WriteString(s.zoneTable.View())
	return content.String()
}

// -----------------------------------------------------------------------------
// Create
// -----------------------------------------------------------------------------

func (s *Service) submitCreateCmd() tea.Cmd {
	name := s.createForm.Value("Zone Name")
	dnsName := s.createForm.Value("DNS Name")
	description := s.createForm.Value("Description")
	visibility := s.createForm.Value("Visibility")
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateZone(s.projectID, name, dnsName, description, visibility); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Zone %s created", name)}
	}
}

// deleteZoneCmd triggers deletion of the given zone
func (s *Service) deleteZoneCmd(zone Zone) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteZone(s.projectID, zone.Name); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting zone %s...", zone.Name)}
	}
}

// deleteRecordCmd deletes a record set from the given zone.
func (s *Service) deleteRecordCmd(zone Zone, record RecordSet) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.DeleteRecordSet(s.projectID, zone.Name, record.Name, record.Type); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleted record %s", record.Name)}
	}
}

// updateZoneCmd patches zone's description.
func (s *Service) updateZoneCmd(zone Zone, description string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateZoneDescription(s.projectID, zone.Name, description); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updated zone %s", zone.Name)}
	}
}

// updateRecordCmd replaces record's TTL and rrdata.
func (s *Service) updateRecordCmd(zone Zone, record RecordSet, ttl int64, rrdatas []string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.UpdateRecordSet(s.projectID, zone.Name, record.Name, record.Type, ttl, rrdatas); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updated record %s", record.Name)}
	}
}

// createRecordCmd creates a new record set in the given zone.
func (s *Service) createRecordCmd(zone Zone, name, recordType string, ttl int64, rrdatas []string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.CreateRecordSet(s.projectID, zone.Name, name, recordType, ttl, rrdatas); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Created record %s", name)}
	}
}

// fetchIAMCmd fetches the current IAM policy for a zone.
func (s *Service) fetchIAMCmd(zone Zone) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return iamPolicyMsg{err: fmt.Errorf("client not initialized")}
		}
		bindings, err := s.client.GetZoneIAMPolicy(s.projectID, zone.Name)
		if err != nil {
			return iamPolicyMsg{err: err}
		}
		return iamPolicyMsg{bindings: bindings}
	}
}

// addIAMBindingCmd grants role to member on the given zone.
func (s *Service) addIAMBindingCmd(zone Zone, role, member string) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized")}
		}
		if err := s.client.AddZoneIAMBinding(s.projectID, zone.Name, role, member); err != nil {
			return actionResultMsg{err: err}
		}
		return actionResultMsg{msg: fmt.Sprintf("Granted %s to %s on zone %s", role, member, zone.Name)}
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

func (s *Service) fetchZonesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dns_zones:%s", s.projectID)

		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]Zone); ok {
					return zonesMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListZones(s.projectID)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return zonesMsg(items)
	}
}

func (s *Service) fetchRecordsCmd(zoneName string) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("dns_records:%s:%s", s.projectID, zoneName)

		if s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if items, ok := val.([]RecordSet); ok {
					return recordsMsg(items)
				}
			}
		}

		if s.client == nil {
			return errMsg(fmt.Errorf("client not init"))
		}

		items, err := s.client.ListRecordSets(s.projectID, zoneName)
		if err != nil {
			return errMsg(err)
		}

		if s.cache != nil {
			s.cache.Set(key, items, CacheTTL)
		}

		return recordsMsg(items)
	}
}

func (s *Service) updateZoneTable(items []Zone) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		desc := item.Description
		if desc == "" {
			desc = "-"
		}
		rows[i] = table.Row{
			item.Name,
			item.DNSName,
			item.Visibility,
			desc,
		}
	}
	s.zoneTable.SetRows(rows)
}

func (s *Service) updateRecordTable(items []RecordSet) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{
			item.Name,
			item.Type,
			fmt.Sprintf("%d", item.TTL),
			strings.Join(item.Rrdatas, ", "),
		}
	}
	s.recordTable.SetRows(rows)
}

// getFilteredZones returns filtered zones based on the query string
func (s *Service) getFilteredZones(zones []Zone, query string) []Zone {
	if query == "" {
		return zones
	}
	return components.FilterSlice(zones, query, func(zone Zone, q string) bool {
		return components.ContainsMatch(zone.Name, zone.DNSName, zone.Visibility, zone.Description)(q)
	})
}
