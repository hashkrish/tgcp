package appengine

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/ui/components"
)

const CacheTTL = 30 * time.Second

// -----------------------------------------------------------------------------
// Models
// -----------------------------------------------------------------------------

type tickMsg time.Time

// ViewState defines the current UI state of the service. Unlike Cloud Run's
// Services/Functions side-by-side tabs, App Engine's Services -> Versions ->
// Instances hierarchy is a drill-down, so there's no TabCycler here.
type ViewState int

const (
	ViewServices ViewState = iota
	ViewVersions
	ViewVersionDetail
	ViewInstances
	ViewApplication
	ViewSplitTraffic
	ViewConfirmation
)

// trafficSplitPattern matches one "version=percent" pair within a
// comma-separated traffic split spec.
var trafficSplitPattern = regexp.MustCompile(`^\s*([^\s=]+)\s*=\s*(\d{1,3})\s*$`)

// parseTrafficSplit parses a comma-separated "v1=50,v2=50" spec into a
// version-ID -> fraction allocation map, validating that percentages are
// 0-100 and sum to exactly 100 (matching `gcloud app services set-traffic`).
func parseTrafficSplit(v string) (map[string]float64, string) {
	parts := strings.Split(v, ",")
	allocations := make(map[string]float64, len(parts))
	total := int64(0)
	for _, part := range parts {
		m := trafficSplitPattern.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Sprintf("invalid entry %q, expected version=percent", strings.TrimSpace(part))
		}
		pct, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil || pct < 0 || pct > 100 {
			return nil, fmt.Sprintf("invalid percent in %q", strings.TrimSpace(part))
		}
		allocations[m[1]] = float64(pct) / 100
		total += pct
	}
	if total != 100 {
		return nil, fmt.Sprintf("percentages must sum to 100, got %d", total)
	}
	return allocations, ""
}

// newTrafficSplitForm builds the FormModel for an N-way traffic split
// across a service's versions, matching
// `gcloud app services set-traffic --splits=v1=.5,v2=.5`.
func newTrafficSplitForm(svc AppEngineService) components.FormModel {
	ids := make([]string, 0, len(svc.Split))
	for id, frac := range svc.Split {
		if frac > 0 {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	current := make([]string, 0, len(ids))
	for _, id := range ids {
		current = append(current, fmt.Sprintf("%s=%d", id, int64(svc.Split[id]*100)))
	}
	return components.NewForm("Split Traffic: "+svc.Id, []components.FormField{
		{
			Label:       "Versions",
			Default:     strings.Join(current, ","),
			Placeholder: "v1=50,v2=50",
			Required:    true,
			Validate: func(v string) string {
				_, errMsg := parseTrafficSplit(v)
				return errMsg
			},
		},
	})
}

type applicationMsg struct{ app *Application }
type servicesMsg []AppEngineService
type versionsMsg struct {
	serviceID string
	versions  []Version
}
type instancesMsg struct {
	serviceID, versionID string
	instances            []Instance
}
type errMsg error

// actionResultMsg carries the result of an async mutating action (delete
// service/version, start/stop version, traffic split).
type actionResultMsg struct {
	err      error
	msg      string
	resource string
	name     string
	action   string
}

// -----------------------------------------------------------------------------
// Service Definition
// -----------------------------------------------------------------------------

// Service implements the services.Service interface for App Engine.
type Service struct {
	client    *Client
	projectID string

	table         *components.StandardTable // Services table
	versionTable  *components.StandardTable // Versions table (selected service)
	instanceTable *components.StandardTable // Instances table (selected version)

	filter               components.FilterModel
	serviceFilterSession components.FilterSession[AppEngineService]
	spinner              components.SpinnerModel

	application *Application
	services    []AppEngineService
	// currentVersions holds the versions currently loaded into versionTable,
	// for cursor-index lookups (mirrors s.services for the services table --
	// there's no filter/session over versions, so this is tracked directly).
	currentVersions []Version
	instances       []Instance
	err             error

	viewState       ViewState
	selectedService *AppEngineService
	selectedVersion *Version

	// Confirmation state
	pendingAction string             // "delete-service", "start-version", "stop-version", "delete-version", "split-traffic"
	pendingSplit  map[string]float64 // staged by the split form, for "split-traffic"
	actionSource  ViewState          // where to return after confirmation/cancel

	splitForm components.FormModel

	cache *core.Cache
}

// NewService creates a new instance of the App Engine service.
func NewService(cache *core.Cache) *Service {
	columns := []table.Column{
		{Title: "Service", Width: 25},
		{Title: "Traffic Split", Width: 50},
	}
	t := components.NewStandardTable(columns)

	versionColumns := []table.Column{
		{Title: "Version", Width: 20},
		{Title: "Status", Width: 12},
		{Title: "Traffic", Width: 10},
		{Title: "Runtime", Width: 14},
		{Title: "Created", Width: 12},
	}
	vt := components.NewStandardTable(versionColumns)

	instanceColumns := []table.Column{
		{Title: "Instance", Width: 30},
		{Title: "VM Status", Width: 12},
		{Title: "Requests", Width: 10},
		{Title: "Errors", Width: 8},
		{Title: "QPS", Width: 8},
	}
	it := components.NewStandardTable(instanceColumns)

	svc := &Service{
		table:         t,
		versionTable:  vt,
		instanceTable: it,
		filter:        components.NewFilterWithPlaceholder("Filter services..."),
		spinner:       components.NewSpinner(),
		viewState:     ViewServices,
		cache:         cache,
	}
	svc.serviceFilterSession = components.NewFilterSession(&svc.filter, svc.getFilteredServices, svc.updateTable)
	return svc
}

func (s *Service) Name() string { return "App Engine" }

func (s *Service) ShortName() string { return "appengine" }

// HelpText returns context-aware keybindings.
func (s *Service) HelpText() string {
	switch s.viewState {
	case ViewServices:
		return "r:Refresh  /:Filter  Ent:Versions  t:Split Traffic  d:Delete  a:Application  l:Logs"
	case ViewVersions:
		return "Esc/q:Back  Ent:Detail  s:Start  x:Stop  d:Delete  I:Instances  l:Logs  r:Refresh"
	case ViewVersionDetail:
		return "Esc/q:Back  s:Start  x:Stop  d:Delete  I:Instances  l:Logs"
	case ViewInstances:
		return "Esc/q:Back  r:Refresh"
	case ViewApplication:
		return "Esc/q:Back"
	case ViewSplitTraffic:
		return "Tab/↑↓:Move  Enter:Submit  Esc:Cancel"
	case ViewConfirmation:
		return "y:Confirm  n:Cancel"
	}
	return ""
}

// -----------------------------------------------------------------------------
// Lifecycle & Interface Implementation
// -----------------------------------------------------------------------------

func (s *Service) InitService(ctx context.Context, projectID string) error {
	s.projectID = projectID
	client, err := NewClient(ctx, projectID)
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
	return tea.Batch(s.tick(), s.Refresh())
}

func (s *Service) tick() tea.Cmd {
	return tea.Tick(CacheTTL, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// Refresh triggers a forced data reload of the services list.
func (s *Service) Refresh() tea.Cmd {
	return tea.Batch(s.spinner.Start(""), s.fetchServicesCmd(true))
}

// Reset clears the service state when navigating away or switching projects.
func (s *Service) Reset() {
	s.viewState = ViewServices
	s.selectedService = nil
	s.selectedVersion = nil
	s.currentVersions = nil
	s.instances = nil
	s.application = nil
	s.pendingAction = ""
	s.pendingSplit = nil
	s.err = nil // CRITICAL: Always clear errors on reset
	s.table.SetCursor(0)
	s.versionTable.SetCursor(0)
	s.instanceTable.SetCursor(0)
	s.filter.ExitFilterMode()
}

// IsRootView returns true if we are at the top-level Services list.
func (s *Service) IsRootView() bool {
	return s.viewState == ViewServices
}

func (s *Service) Focus() {
	s.table.Focus()
	s.versionTable.Focus()
	s.instanceTable.Focus()
}

func (s *Service) Blur() {
	s.table.Blur()
	s.versionTable.Blur()
	s.instanceTable.Blur()
}

// -----------------------------------------------------------------------------
// Update Loop
// -----------------------------------------------------------------------------

func (s *Service) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case components.SpinnerTickMsg:
		s.spinner, cmd = s.spinner.Update(msg)
		return s, cmd

	case tickMsg:
		return s, tea.Batch(s.fetchServicesCmd(false), s.tick())

	case applicationMsg:
		s.spinner.Stop()
		s.err = nil
		s.application = msg.app
		s.viewState = ViewApplication
		return s, nil

	case servicesMsg:
		s.spinner.Stop()
		s.err = nil
		s.services = msg
		s.serviceFilterSession.Apply(s.services)
		if s.selectedService != nil {
			for i := range s.services {
				if s.services[i].Id == s.selectedService.Id {
					s.selectedService = &s.services[i]
					break
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case versionsMsg:
		s.spinner.Stop()
		s.err = nil
		if s.selectedService != nil && s.selectedService.Id == msg.serviceID {
			s.updateVersionTable(msg.versions)
			if s.selectedVersion != nil {
				for i := range msg.versions {
					if msg.versions[i].Id == s.selectedVersion.Id {
						v := msg.versions[i]
						s.selectedVersion = &v
						break
					}
				}
			}
		}
		return s, func() tea.Msg { return core.LastUpdatedMsg(time.Now()) }

	case instancesMsg:
		s.spinner.Stop()
		s.err = nil
		if s.selectedService != nil && s.selectedService.Id == msg.serviceID &&
			s.selectedVersion != nil && s.selectedVersion.Id == msg.versionID {
			s.instances = msg.instances
			s.updateInstanceTable(msg.instances)
		}
		return s, nil

	case errMsg:
		s.spinner.Stop()
		s.err = msg
		return s, nil

	case actionResultMsg:
		if msg.err != nil {
			return s, func() tea.Msg { return core.ToastMsg{Message: msg.err.Error(), Type: core.ToastError} }
		}
		var cmds []tea.Cmd
		if msg.msg != "" {
			cmds = append(cmds, func() tea.Msg { return core.ToastMsg{Message: msg.msg, Type: core.ToastSuccess} })
		}
		cmds = append(cmds, s.fetchServicesCmd(true))
		if s.selectedService != nil {
			cmds = append(cmds, s.fetchVersionsCmd(*s.selectedService, true))
		}
		return s, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		s.table.HandleWindowSizeDefault(msg)
		s.versionTable.HandleWindowSizeDefault(msg)
		s.instanceTable.HandleWindowSizeDefault(msg)

	case tea.MouseMsg:
		switch s.viewState {
		case ViewServices:
			var ut *components.StandardTable
			ut, cmd = s.table.Update(msg)
			s.table = ut
			return s, cmd
		case ViewVersions:
			var ut *components.StandardTable
			ut, cmd = s.versionTable.Update(msg)
			s.versionTable = ut
			return s, cmd
		case ViewInstances:
			var ut *components.StandardTable
			ut, cmd = s.instanceTable.Update(msg)
			s.instanceTable = ut
			return s, cmd
		}

	case tea.KeyMsg:
		if s.viewState == ViewServices {
			result := s.serviceFilterSession.HandleKey(msg)
			if result.Handled {
				if result.Cmd != nil {
					return s, result.Cmd
				}
				if !result.ShouldContinue {
					return s, nil
				}
			}
		}

		switch s.viewState {
		case ViewServices:
			switch msg.String() {
			case "r":
				return s, s.Refresh()
			case "a":
				return s, tea.Batch(s.spinner.Start(""), s.fetchApplicationCmd())
			case "enter":
				svcs := s.getFilteredServices(s.services, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
					sel := svcs[idx]
					s.selectedService = &sel
					s.viewState = ViewVersions
					return s, tea.Batch(s.spinner.Start(""), s.fetchVersionsCmd(sel, false))
				}
			case "t":
				svcs := s.getFilteredServices(s.services, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
					sel := svcs[idx]
					s.selectedService = &sel
					s.splitForm = newTrafficSplitForm(sel)
					s.actionSource = ViewServices
					s.viewState = ViewSplitTraffic
				}
			case "d":
				svcs := s.getFilteredServices(s.services, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
					sel := svcs[idx]
					s.selectedService = &sel
					s.pendingAction = "delete-service"
					s.actionSource = ViewServices
					s.viewState = ViewConfirmation
				}
			case "l":
				svcs := s.getFilteredServices(s.services, s.filter.Value())
				if idx := s.table.Cursor(); idx >= 0 && idx < len(svcs) {
					sel := svcs[idx]
					filter := fmt.Sprintf(`resource.type="gae_app" AND resource.labels.module_id="%s"`, sel.Id)
					heading := fmt.Sprintf("Service: %s", sel.Id)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "appengine", Heading: heading} }
				}
			}
			var ut *components.StandardTable
			ut, cmd = s.table.Update(msg)
			s.table = ut
			return s, cmd

		case ViewVersions:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewServices
				s.selectedVersion = nil
				return s, nil
			case "r":
				if s.selectedService != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchVersionsCmd(*s.selectedService, true))
				}
			case "enter":
				if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
					v := s.currentVersions[idx]
					s.selectedVersion = &v
					s.viewState = ViewVersionDetail
				}
				return s, nil
			case "s":
				if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
					v := s.currentVersions[idx]
					s.selectedVersion = &v
					s.pendingAction = "start-version"
					s.actionSource = ViewVersions
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x":
				if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
					v := s.currentVersions[idx]
					s.selectedVersion = &v
					s.pendingAction = "stop-version"
					s.actionSource = ViewVersions
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d":
				if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
					v := s.currentVersions[idx]
					s.selectedVersion = &v
					s.pendingAction = "delete-version"
					s.actionSource = ViewVersions
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "I":
				if s.selectedService != nil {
					if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
						v := s.currentVersions[idx]
						s.selectedVersion = &v
						s.viewState = ViewInstances
						return s, tea.Batch(s.spinner.Start(""), s.fetchInstancesCmd(*s.selectedService, v))
					}
				}
				return s, nil
			case "l":
				if s.selectedService != nil {
					if idx := s.versionTable.Cursor(); idx >= 0 && idx < len(s.currentVersions) {
						v := s.currentVersions[idx]
						filter := fmt.Sprintf(`resource.type="gae_app" AND resource.labels.module_id="%s" AND resource.labels.version_id="%s"`, s.selectedService.Id, v.Id)
						heading := fmt.Sprintf("Version: %s/%s", s.selectedService.Id, v.Id)
						return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "appengine", Heading: heading} }
					}
				}
				return s, nil
			}
			var ut *components.StandardTable
			ut, cmd = s.versionTable.Update(msg)
			s.versionTable = ut
			return s, cmd

		case ViewVersionDetail:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewVersions
				return s, nil
			case "s":
				if s.selectedVersion != nil {
					s.pendingAction = "start-version"
					s.actionSource = ViewVersionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "x":
				if s.selectedVersion != nil {
					s.pendingAction = "stop-version"
					s.actionSource = ViewVersionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "d":
				if s.selectedVersion != nil {
					s.pendingAction = "delete-version"
					s.actionSource = ViewVersionDetail
					s.viewState = ViewConfirmation
				}
				return s, nil
			case "I":
				if s.selectedService != nil && s.selectedVersion != nil {
					s.viewState = ViewInstances
					return s, tea.Batch(s.spinner.Start(""), s.fetchInstancesCmd(*s.selectedService, *s.selectedVersion))
				}
				return s, nil
			case "l":
				if s.selectedService != nil && s.selectedVersion != nil {
					filter := fmt.Sprintf(`resource.type="gae_app" AND resource.labels.module_id="%s" AND resource.labels.version_id="%s"`, s.selectedService.Id, s.selectedVersion.Id)
					heading := fmt.Sprintf("Version: %s/%s", s.selectedService.Id, s.selectedVersion.Id)
					return s, func() tea.Msg { return core.SwitchToLogsMsg{Filter: filter, Source: "appengine", Heading: heading} }
				}
				return s, nil
			}
			return s, nil

		case ViewInstances:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewVersionDetail
				return s, nil
			case "r":
				if s.selectedService != nil && s.selectedVersion != nil {
					return s, tea.Batch(s.spinner.Start(""), s.fetchInstancesCmd(*s.selectedService, *s.selectedVersion))
				}
				return s, nil
			}
			var ut *components.StandardTable
			ut, cmd = s.instanceTable.Update(msg)
			s.instanceTable = ut
			return s, cmd

		case ViewApplication:
			switch msg.String() {
			case "esc", "q":
				s.viewState = ViewServices
				return s, nil
			}
			return s, nil

		case ViewSplitTraffic:
			result, fcmd := s.splitForm.Update(msg)
			if result.Cancelled {
				s.viewState = s.actionSource
				return s, nil
			}
			if result.Submitted {
				vals := s.splitForm.Values()
				allocations, errStr := parseTrafficSplit(vals["Versions"])
				if errStr != "" {
					return s, nil
				}
				s.pendingSplit = allocations
				s.pendingAction = "split-traffic"
				s.viewState = ViewConfirmation
				return s, nil
			}
			return s, fcmd

		case ViewConfirmation:
			switch msg.String() {
			case "y", "enter":
				var actionCmd tea.Cmd
				switch s.pendingAction {
				case "delete-service":
					if s.selectedService != nil {
						actionCmd = s.deleteServiceCmd(*s.selectedService)
					}
					s.viewState = ViewServices
					s.selectedService = nil
				case "start-version":
					if s.selectedService != nil && s.selectedVersion != nil {
						actionCmd = s.setServingStatusCmd(*s.selectedService, *s.selectedVersion, "SERVING")
					}
					s.viewState = s.actionSource
				case "stop-version":
					if s.selectedService != nil && s.selectedVersion != nil {
						actionCmd = s.setServingStatusCmd(*s.selectedService, *s.selectedVersion, "STOPPED")
					}
					s.viewState = s.actionSource
				case "delete-version":
					if s.selectedService != nil && s.selectedVersion != nil {
						actionCmd = s.deleteVersionCmd(*s.selectedService, *s.selectedVersion)
					}
					s.viewState = ViewVersions
					s.selectedVersion = nil
				case "split-traffic":
					if s.selectedService != nil {
						actionCmd = s.setTrafficSplitCmd(*s.selectedService, s.pendingSplit)
					}
					s.viewState = ViewServices
				}
				s.pendingAction = ""
				s.pendingSplit = nil
				return s, actionCmd
			case "n", "esc", "q":
				s.viewState = s.actionSource
				s.pendingAction = ""
				s.pendingSplit = nil
				return s, nil
			}
			return s, nil
		}
	}

	return s, cmd
}

// -----------------------------------------------------------------------------
// Commands
// -----------------------------------------------------------------------------

func (s *Service) fetchServicesCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("appengine_services:%s", s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if svcs, ok := val.([]AppEngineService); ok {
					return servicesMsg(svcs)
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		svcs, err := s.client.ListServices(context.Background())
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, svcs, CacheTTL)
		}
		return servicesMsg(svcs)
	}
}

func (s *Service) fetchApplicationCmd() tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		app, err := s.client.GetApplication(context.Background())
		if err != nil {
			return errMsg(err)
		}
		return applicationMsg{app: app}
	}
}

// fetchVersionsCmd fetches a service's versions, cached per-service for
// CacheTTL unless force is set (used after a mutating version/traffic
// action so the view reflects the change instead of a stale cache entry).
func (s *Service) fetchVersionsCmd(svc AppEngineService, force bool) tea.Cmd {
	return func() tea.Msg {
		key := fmt.Sprintf("appengine_versions_%s:%s", svc.Id, s.projectID)
		if !force && s.cache != nil {
			if val, found := s.cache.Get(key); found {
				if versions, ok := val.([]Version); ok {
					return versionsMsg{serviceID: svc.Id, versions: versions}
				}
			}
		}
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		versions, err := s.client.ListVersions(context.Background(), svc.Id, svc.Split)
		if err != nil {
			return errMsg(err)
		}
		if s.cache != nil {
			s.cache.Set(key, versions, CacheTTL)
		}
		return versionsMsg{serviceID: svc.Id, versions: versions}
	}
}

func (s *Service) fetchInstancesCmd(svc AppEngineService, ver Version) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return errMsg(fmt.Errorf("client not initialized"))
		}
		instances, err := s.client.ListInstances(context.Background(), svc.Id, ver.Id)
		if err != nil {
			return errMsg(err)
		}
		return instancesMsg{serviceID: svc.Id, versionID: ver.Id, instances: instances}
	}
}

func (s *Service) deleteServiceCmd(svc AppEngineService) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "service", Name: svc.Id, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteService(context.Background(), svc.Id)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "service", name: svc.Id, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting service %s...", svc.Id), resource: "service", name: svc.Id, action: "delete"}
	}
}

func (s *Service) setServingStatusCmd(svc AppEngineService, ver Version, status string) tea.Cmd {
	verb, action := "Starting", "start-version"
	if status == "STOPPED" {
		verb, action = "Stopping", "stop-version"
	}
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "version", Name: ver.Id, Action: action,
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.SetVersionServingStatus(context.Background(), svc.Id, ver.Id, status)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "version", name: ver.Id, action: action}
		}
		return actionResultMsg{msg: fmt.Sprintf("%s version %s...", verb, ver.Id), resource: "version", name: ver.Id, action: action}
	}
}

func (s *Service) deleteVersionCmd(svc AppEngineService, ver Version) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "version", Name: ver.Id, Action: "delete",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.DeleteVersion(context.Background(), svc.Id, ver.Id)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "version", name: ver.Id, action: "delete"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Deleting version %s...", ver.Id), resource: "version", name: ver.Id, action: "delete"}
	}
}

func (s *Service) setTrafficSplitCmd(svc AppEngineService, allocations map[string]float64) tea.Cmd {
	return func() tea.Msg {
		err := core.TrackJob(core.Job{
			ProjectID: s.projectID, Service: s.ShortName(), Resource: "service", Name: svc.Id, Action: "split-traffic",
		}, func() error {
			if s.client == nil {
				return fmt.Errorf("client not initialized")
			}
			return s.client.SetTrafficSplit(context.Background(), svc.Id, allocations)
		})
		if err != nil {
			return actionResultMsg{err: err, resource: "service", name: svc.Id, action: "split-traffic"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Updated traffic split for %s", svc.Id), resource: "service", name: svc.Id, action: "split-traffic"}
	}
}

// -----------------------------------------------------------------------------
// Tables / Filtering
// -----------------------------------------------------------------------------

func (s *Service) updateTable(items []AppEngineService) {
	rows := make([]table.Row, len(items))
	for i, item := range items {
		rows[i] = table.Row{item.Id, formatSplit(item.Split)}
	}
	s.table.SetRows(rows)
}

func (s *Service) updateVersionTable(versions []Version) {
	s.currentVersions = versions
	rows := make([]table.Row, len(versions))
	for i, v := range versions {
		created := ""
		if !v.CreateTime.IsZero() {
			created = v.CreateTime.Format("2006-01-02")
		}
		traffic := "-"
		if v.Traffic > 0 {
			traffic = fmt.Sprintf("%.0f%%", v.Traffic*100)
		}
		rows[i] = table.Row{v.Id, v.ServingStatus, traffic, v.Runtime, created}
	}
	s.versionTable.SetRows(rows)
}

func (s *Service) updateInstanceTable(instances []Instance) {
	rows := make([]table.Row, len(instances))
	for i, inst := range instances {
		rows[i] = table.Row{inst.Id, inst.VmStatus, fmt.Sprintf("%d", inst.Requests), fmt.Sprintf("%d", inst.Errors), fmt.Sprintf("%.1f", inst.Qps)}
	}
	s.instanceTable.SetRows(rows)
}

func (s *Service) getFilteredServices(items []AppEngineService, query string) []AppEngineService {
	if query == "" {
		return items
	}
	return components.FilterSlice(items, query, func(svc AppEngineService, q string) bool {
		return components.ContainsMatch(svc.Id)(q)
	})
}

// formatSplit renders a service's traffic split as e.g. "v1:80% v2:20%",
// omitting versions with a zero allocation.
func formatSplit(split map[string]float64) string {
	if len(split) == 0 {
		return "-"
	}
	ids := make([]string, 0, len(split))
	for id := range split {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		if frac := split[id]; frac > 0 {
			parts = append(parts, fmt.Sprintf("%s:%.0f%%", id, frac*100))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}
