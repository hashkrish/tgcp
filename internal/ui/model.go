package ui

import (
	"context"
	"fmt"
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/config"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/services"
	"github.com/yogirk/tgcp/internal/services/artifactregistry"
	"github.com/yogirk/tgcp/internal/services/bigquery"
	"github.com/yogirk/tgcp/internal/services/bigtable"
	"github.com/yogirk/tgcp/internal/services/cloudbuild"
	"github.com/yogirk/tgcp/internal/services/cloudfunctions"
	"github.com/yogirk/tgcp/internal/services/cloudrun"
	"github.com/yogirk/tgcp/internal/services/cloudsql"
	"github.com/yogirk/tgcp/internal/services/cloudtasks"
	"github.com/yogirk/tgcp/internal/services/dataflow"
	"github.com/yogirk/tgcp/internal/services/dataproc"
	"github.com/yogirk/tgcp/internal/services/disks"
	"github.com/yogirk/tgcp/internal/services/dns"
	"github.com/yogirk/tgcp/internal/services/filestore"
	"github.com/yogirk/tgcp/internal/services/firestore"
	"github.com/yogirk/tgcp/internal/services/gce"
	"github.com/yogirk/tgcp/internal/services/gcs"
	"github.com/yogirk/tgcp/internal/services/gke"
	"github.com/yogirk/tgcp/internal/services/iam"
	"github.com/yogirk/tgcp/internal/services/ipaddress"
	"github.com/yogirk/tgcp/internal/services/jobs"
	"github.com/yogirk/tgcp/internal/services/kms"
	"github.com/yogirk/tgcp/internal/services/loadbalancing"
	"github.com/yogirk/tgcp/internal/services/logging"
	"github.com/yogirk/tgcp/internal/services/monitoring"
	"github.com/yogirk/tgcp/internal/services/net"
	"github.com/yogirk/tgcp/internal/services/overview"
	"github.com/yogirk/tgcp/internal/services/parametermanager"
	"github.com/yogirk/tgcp/internal/services/pubsub"
	"github.com/yogirk/tgcp/internal/services/redis"
	"github.com/yogirk/tgcp/internal/services/scheduler"
	"github.com/yogirk/tgcp/internal/services/secrets"
	"github.com/yogirk/tgcp/internal/services/spanner"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// ViewMode defines the high-level view state
type ViewMode int

const (
	ViewHome ViewMode = iota
	ViewService
)

// FocusArea defines where the user input is directed
type FocusArea int

const (
	FocusSidebar FocusArea = iota
	FocusMain
	FocusPalette
)

// MainModel is the main application state
type MainModel struct {
	AuthState  core.AuthState
	Navigation core.NavigationModel

	// Layout
	Width  int
	Height int

	// Services
	ServiceMap map[string]services.Service
	CurrentSvc services.Service

	// Components
	Sidebar   components.SidebarModel
	HomeMenu  components.HomeMenuModel // Added
	StatusBar components.StatusBarModel
	Palette   components.PaletteModel // Added
	Toast     *components.ToastModel  // Toast notification (nil when hidden)
	Spinner   components.SpinnerModel // Global loading spinner

	// State
	ViewMode      ViewMode // Added
	Focus         FocusArea
	LastFocus     FocusArea
	ShowHelp      bool
	ActiveService string

	// External Managers
	ProjectManager  *core.ProjectManager
	ServiceRegistry *core.ServiceRegistry
	Cache           *core.Cache

	// Config is the loaded ~/.tgcprc, kept around (rather than only consulted
	// at startup) so runtime features like the configured-projects quick
	// switcher (Ctrl+g) can read it.
	Config *config.Config

	// Version Info
	Version    core.VersionInfo
	UpdateInfo *core.UpdateInfo // nil until checked

	// LandingStats is a lightweight resource-count snapshot shown on the
	// landing page. nil until the one-shot background fetch (see Init())
	// completes; deliberately never re-fetched on a timer -- staleness
	// until app restart is an acceptable tradeoff for a glanceable count,
	// not a live dashboard.
	LandingStats *overview.ResourceInventory
}

// InitialModel returns the initial state of the application
func InitialModel(authState core.AuthState, cfg *config.Config, version core.VersionInfo) MainModel {
	// Initialize Cache
	cache := core.NewCache()

	// Create service registry and register all services
	registry := core.NewServiceRegistry(cache)
	registerAllServices(registry)

	// Create service map but don't initialize services yet (lazy initialization)
	// Services will be initialized on first access
	svcMap := registry.InitializeAll(context.Background(), authState.ProjectID)

	// Initialize Components
	sb := components.NewSidebar()
	sb.Visible = cfg.UI.SidebarVisible
	statusBar := components.NewStatusBar()
	statusBar.SetFocusPane("HOME")

	nav := core.NewNavigation()
	nav.SetBaseCommands(append(nav.Commands, serviceCommands(svcMap)...))

	return MainModel{
		AuthState:       authState,
		Navigation:      nav,
		Sidebar:         sb,
		HomeMenu:        components.NewHomeMenu(),
		StatusBar:       statusBar,
		Palette:         components.NewPalette(),
		Spinner:         components.NewSpinner(),
		Focus:           FocusSidebar,
		ViewMode:        ViewHome,
		ServiceMap:      svcMap,
		ProjectManager:  core.NewProjectManager(cache),
		ServiceRegistry: registry,
		Cache:           cache,
		Config:          cfg,
		Version:         version,
	}
}

// Init initializes the bubbletea program
func (m MainModel) Init() tea.Cmd {
	// Start with mouse support and check for updates in background
	return tea.Batch(
		tea.EnableMouseCellMotion,
		core.CheckForUpdates(m.Version.Version),
		m.fetchLandingStatsCmd(),
	)
}

// landingStatsMsg carries the one-shot resource-count snapshot for the
// landing page's stats strip.
type landingStatsMsg overview.ResourceInventory

// fetchLandingStatsCmd fetches a lightweight resource-count snapshot for the
// landing page's stats strip. Reuses the exact same cache key format the
// real Overview service uses for its own inventory fetch
// (internal/services/overview/overview.go fetchInventoryCmd) so opening the
// Overview tab later hits the same cache entry instead of double-fetching.
func (m MainModel) fetchLandingStatsCmd() tea.Cmd {
	return func() tea.Msg {
		cacheKey := fmt.Sprintf("billing:inventory:global:%s", m.AuthState.ProjectID)
		if m.Cache != nil {
			if val, found := m.Cache.Get(cacheKey); found {
				if inv, ok := val.(overview.ResourceInventory); ok {
					return landingStatsMsg(inv)
				}
			}
		}

		client, err := overview.NewClient(context.Background())
		if err != nil {
			return nil // best-effort; landing page just shows no stats strip
		}
		inv, err := client.GetGlobalInventory(m.AuthState.ProjectID)
		if err != nil {
			return nil
		}
		if m.Cache != nil {
			m.Cache.Set(cacheKey, inv, overview.CacheTTL)
		}
		return landingStatsMsg(inv)
	}
}

// getOrInitializeService gets a service from the map, initializing it lazily if needed
// This implements lazy initialization - services are only initialized when first accessed
func (m *MainModel) getOrInitializeService(ctx context.Context, serviceName string) (services.Service, error) {
	// First check if service exists in map
	if svc, exists := m.ServiceMap[serviceName]; exists {
		// Service exists, but may not be initialized yet
		// Use registry to ensure it's initialized
		if m.ServiceRegistry != nil {
			initializedSvc, err := m.ServiceRegistry.GetOrInitializeService(ctx, serviceName)
			if err != nil {
				return svc, err // Return original service if init fails
			}
			if initializedSvc != nil {
				// Update the map with the initialized service
				m.ServiceMap[serviceName] = initializedSvc
				return initializedSvc, nil
			}
		}
		return svc, nil
	}

	// Service doesn't exist in map - try to get it from registry (lazy creation)
	if m.ServiceRegistry != nil {
		svc, err := m.ServiceRegistry.GetOrInitializeService(ctx, serviceName)
		if err != nil {
			return nil, err
		}
		if svc != nil {
			// Add to map
			m.ServiceMap[serviceName] = svc
			return svc, nil
		}
	}

	return nil, nil // Service not found
}

// showConfiguredProjectSwitcher populates the palette with the projects
// listed under `projects:` in ~/.tgcprc, so the user can jump straight to one
// of their known projects without waiting on a Cloud Resource Manager API
// call. Selecting an entry routes through the same "SWITCH_PROJECT:<id>"
// mechanism as the API-backed switcher.
func (m *MainModel) showConfiguredProjectSwitcher() {
	var cmds []core.Command
	if m.Config != nil {
		for _, p := range m.Config.Projects {
			p := p // capture
			label := p.Name
			if label == "" {
				label = p.ID
			}
			cmds = append(cmds, core.Command{
				Name:        p.ID,
				Description: label,
				Action: func() core.Route {
					return core.Route{View: core.ViewHome, ID: "SWITCH_PROJECT:" + p.ID}
				},
			})
		}
	}

	if len(cmds) == 0 {
		m.StatusBar.Message = "No projects configured — add a `projects:` list to ~/.tgcprc"
		return
	}

	m.Navigation.SetCommands(cmds)
	m.StatusBar.Message = "Switch to configured project..."
}

// Update handles messages and updates the model
func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd
	defer m.syncStatusBarFocus()

	switch msg := msg.(type) {
	// Toast Notifications
	case core.ToastMsg:
		m.Toast = components.NewToastFromMsg(msg)
		return m, m.Toast.DismissCmd()

	case components.ToastDismissMsg:
		if m.Toast != nil && m.Toast.CreatedAt.Equal(msg.CreatedAt) {
			m.Toast = nil
		}
		return m, nil

	// Version Update Check
	case core.UpdateCheckedMsg:
		m.UpdateInfo = &msg.UpdateInfo
		return m, nil

	// Loading Spinner
	case core.LoadingMsg:
		if msg.IsLoading {
			cmd = m.Spinner.Start(msg.Message)
			return m, cmd
		} else {
			m.Spinner.Stop()
			return m, nil
		}

	case components.SpinnerTickMsg:
		var cmds []tea.Cmd
		// Update main model spinner
		m.Spinner, cmd = m.Spinner.Update(msg)
		cmds = append(cmds, cmd)
		// Also forward to current service for its own spinner animation
		if m.ViewMode == ViewService && m.CurrentSvc != nil {
			var newModel tea.Model
			newModel, cmd = m.CurrentSvc.Update(msg)
			if updatedSvc, ok := newModel.(services.Service); ok {
				m.CurrentSvc = updatedSvc
				m.ServiceMap[m.CurrentSvc.ShortName()] = updatedSvc
			}
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)

	// Status Bar Updates
	case core.StatusMsg:
		m.StatusBar.Message = msg.Message
		m.StatusBar.IsError = msg.IsError
		return m, nil

	case core.LastUpdatedMsg:
		m.StatusBar.LastUpdated = time.Time(msg)
		return m, nil

	case landingStatsMsg:
		inv := overview.ResourceInventory(msg)
		m.LandingStats = &inv
		return m, nil

	case tea.KeyMsg:
		// Global Keybindings
		if m.Focus != FocusPalette {
			switch msg.String() {
			case "q", "esc":
				if m.ShowHelp {
					m.ShowHelp = false
					return m, nil
				}
				if m.ViewMode == ViewHome {
					// Don't quit if user is typing in filter
					if m.HomeMenu.FilterActive() {
						break
					}
					// Let home menu handle clearing the filter
					if m.HomeMenu.HasFilter() {
						break
					}
					return m, tea.Quit
				}
				// If in Service Mode, we delegate to the service specific block below
				// or if IsRootView logic there handles it.
				// But wait, if we are in Service Mode and IsRootView is true, we want Home.
				// The block below handles it. But if we are here, we need to NOT Quit.
				// Effectively, if ViewService, don't do anything here, let next block handle.
				if m.ViewMode == ViewService {
					// Fallthrough to Service Loop
				} else {
					return m, tea.Quit
				}
			case "ctrl+c":
				return m, tea.Quit
			case "ctrl+l":
				// Manual force-redraw: some terminal multiplexers can desync
				// their own screen buffer from bubbletea's diffed output
				// under bursts of rapid input, leaving stale content on
				// screen. Ctrl+L is the standard terminal convention for
				// "redraw" and forces a full repaint to recover.
				return m, tea.ClearScreen
			case ":":
				if m.ViewMode == ViewHome && m.HomeMenu.FilterActive() {
					break // let filter input receive ":"
				}
				m.LastFocus = m.Focus
				m.setFocus(FocusPalette)
				m.Navigation.PaletteActive = true
				m.StatusBar.Mode = "COMMAND"
				m.StatusBar.Message = "Type command..."
				return m, nil
			case "?":
				if m.ViewMode == ViewHome && m.HomeMenu.FilterActive() {
					break // let filter input receive "?"
				}
				m.ShowHelp = !m.ShowHelp
				return m, nil
			case "ctrl+g":
				// Quick-switch between projects defined in ~/.tgcprc --
				// distinct from the ":" command palette's API-backed "GCP:
				// Switch Project", which lists every project the account can
				// see and requires a network round-trip.
				if m.ViewMode == ViewHome && m.HomeMenu.FilterActive() {
					break // let filter input receive it, consistent with ":" and "?"
				}
				m.showConfiguredProjectSwitcher()
				m.LastFocus = m.Focus
				m.setFocus(FocusPalette)
				m.Navigation.PaletteActive = true
				m.StatusBar.Mode = "COMMAND"
				return m, nil
			case "ctrl+b":
				// Sidebar show/hide -- moved off Tab (see below) since Tab
				// now cycles the active service's own tabs instead.
				if m.ViewMode == ViewService {
					m.Sidebar.Visible = !m.Sidebar.Visible
					// Adjust focus if hiding active sidebar
					if !m.Sidebar.Visible && m.Focus == FocusSidebar {
						m.setFocus(FocusMain)
						m.Sidebar.Active = false
					}

					// Re-sync the active service's width now that the
					// sidebar visibility (and thus available width) changed.
					if m.CurrentSvc != nil && m.Width > 0 && m.Height > 0 {
						availWidth := m.Width
						if m.Sidebar.Visible {
							availWidth -= m.Sidebar.Width
						}
						newModel, svcCmd := m.CurrentSvc.Update(tea.WindowSizeMsg{
							Width:  availWidth,
							Height: m.Height,
						})
						if updatedSvc, ok := newModel.(services.Service); ok {
							m.CurrentSvc = updatedSvc
							m.ServiceMap[m.CurrentSvc.ShortName()] = updatedSvc
						}
						return m, svcCmd
					}
				}
				return m, nil
			case "tab", "shift+tab":
				// Cycle the active service's own tabs (e.g. Cloud Run's
				// Services/Functions, Load Balancing's 5 resource tabs) --
				// exactly equivalent to pressing "]"/"[", which each such
				// service already handles internally. Forwarding a
				// synthetic KeyMsg (rather than teaching every tabbed
				// service a new "tab"/"shift+tab" case) keeps this a
				// one-place change.
				//
				// Only do this remapping at the service's root (list) view.
				// Away from root -- e.g. a create/update form -- Tab already
				// means "next field" (components.FormModel.Update handles
				// "tab"/"shift+tab" itself), and remapping it to "]"/"["
				// there fed the form a synthetic KeyRunes event that its
				// text input just inserted as a literal character instead
				// of moving focus.
				if m.ViewMode == ViewService && m.CurrentSvc != nil && m.CurrentSvc.IsRootView() {
					key := "]"
					if msg.String() == "shift+tab" {
						key = "["
					}
					newModel, svcCmd := m.CurrentSvc.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
					if updatedSvc, ok := newModel.(services.Service); ok {
						m.CurrentSvc = updatedSvc
						m.ServiceMap[m.CurrentSvc.ShortName()] = updatedSvc
					}
					return m, svcCmd
				}
				// Not at root view (or no active service/service view): let
				// the real Tab/Shift+Tab key fall through to the normal
				// service-forwarding path below instead of being consumed
				// here.
			}
		} else {
			// Palette specific keys (Esc to close)
			// Palette specific keys (Esc to close)
			switch msg.String() {
			case "esc":
				m.setFocus(m.LastFocus)
				m.Navigation.PaletteActive = false
				m.StatusBar.Message = "Ready"
				m.Palette.TextInput.Reset()        // Clear input
				m.Navigation.RestoreBaseCommands() // Reset to default commands
				m.Navigation.FilterCommands("")
				return m, nil
			case "up", "ctrl+p":
				m.Navigation.SelectPrev()
				return m, nil
			case "down", "ctrl+n":
				m.Navigation.SelectNext()
				return m, nil
			case "enter":
				// Execute Command
				if route := m.Navigation.ExecuteSelection(); route != nil {
					// Route Logic
					switch route.View {
					case core.ViewHome:
						// Check for Project Switch
						if len(route.ID) > 15 && route.ID[:15] == "SWITCH_PROJECT:" {
							newProjectID := route.ID[15:]
							m.AuthState.ProjectID = newProjectID

							// Re-initialize all services with new project using registry
							if m.ServiceRegistry != nil {
								m.ServiceRegistry.ReinitializeAll(context.Background(), newProjectID, m.ServiceMap)
							}

							m.StatusBar.Message = "Switched to project: " + newProjectID
							m.Navigation.RestoreBaseCommands()
							m.ViewMode = ViewHome
							m.Sidebar.Active = false
						} else {
							m.ViewMode = ViewHome
							m.Sidebar.Active = false
						}
					case core.ViewServiceList:
						// Logic to switch service
						m.ViewMode = ViewService
						m.ActiveService = route.Service
						// Sync Sidebar
						for i, item := range m.Sidebar.Items {
							if item.ShortName == route.Service {
								m.Sidebar.Cursor = i
							}
						}
						// Get or initialize service lazily
						svc, err := m.getOrInitializeService(context.Background(), m.ActiveService)
						if err != nil {
							cmds = append(cmds, func() tea.Msg {
								return core.StatusMsg{Message: "Failed to initialize service: " + err.Error(), IsError: true}
							})
						} else if svc != nil {
							svc.Reset()
							if route.SubTab != "" {
								if ts, ok := svc.(tabbedService); ok {
									if _, tabCmd := ts.SetActiveTab(route.SubTab); tabCmd != nil {
										cmds = append(cmds, tabCmd)
									}
								}
							}
							m.CurrentSvc = svc

							// Sync Window Size
							if m.Width > 0 && m.Height > 0 {
								newModel, _ := svc.Update(tea.WindowSizeMsg{
									Width:  m.Width,
									Height: m.Height,
								})
								if updatedSvc, ok := newModel.(services.Service); ok {
									svc = updatedSvc
									m.ServiceMap[m.ActiveService] = svc
									m.CurrentSvc = svc
								}
							}

							// trigger refresh
							cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
						}
						m.setFocus(FocusMain)
						m.Sidebar.Active = false
					case core.ViewProjectSwitcher:
						// Trigger fetch projects
						cmds = append(cmds, func() tea.Msg {
							projects, err := m.ProjectManager.ListProjects(context.Background())
							if err != nil {
								return core.StatusMsg{Message: "Failed to list projects: " + err.Error(), IsError: true}
							}
							return projects
						})
						// Keep palette open? Yes.
						// Status update?
						m.StatusBar.Message = "Fetching projects..."
						m.setFocus(FocusPalette)
						return m, tea.Batch(cmds...)
					case core.ViewQuickProjectSwitcher:
						// No API call needed -- list is synchronous, from config.
						m.showConfiguredProjectSwitcher()
						m.setFocus(FocusPalette)
						return m, nil
					}
					// Close Palette. Which focus to restore depends on where we
					// just navigated to -- NOT always m.LastFocus (the focus
					// from BEFORE the palette opened): a ViewServiceList route
					// already set FocusMain above (so its table/detail view
					// receives keypresses), and clobbering that with
					// m.LastFocus here was a real bug -- e.g. opening the
					// palette from the landing page (LastFocus == FocusSidebar)
					// and jumping straight into a service left the new
					// service's own view completely unable to receive
					// up/down/etc., because focus silently reverted to
					// whatever it was before the palette ever opened.
					switch route.View {
					case core.ViewHome:
						m.setFocus(FocusSidebar) // or menu
					case core.ViewServiceList:
						// Already set to FocusMain above; leave it alone.
					default:
						m.setFocus(m.LastFocus)
					}

					m.Navigation.PaletteActive = false
					m.StatusBar.Mode = "NORMAL"
					m.StatusBar.Message = "Ready"
					m.Palette.TextInput.Reset()
					m.Navigation.FilterCommands("")
				}
				return m, tea.Batch(cmds...)
			}

			// Forward other keys to Palette Input
			var cmd tea.Cmd
			m.Palette, cmd = m.Palette.Update(msg)
			cmds = append(cmds, cmd)

			// Update Suggestions
			if m.Palette.TextInput.Value() != m.Navigation.Query {
				m.Navigation.FilterCommands(m.Palette.TextInput.Value())
			}

			return m, tea.Batch(cmds...)
		}

		// HOME MODE
		if m.ViewMode == ViewHome && !m.ShowHelp && m.Focus != FocusPalette {
			switch msg.String() {
			case "enter":
				// Select service
				selected := m.HomeMenu.SelectedItem()
				if selected.ShortName != "" && !selected.IsComing { // Only allow entering implemented services
					m.ViewMode = ViewService
					m.ActiveService = selected.ShortName

					// Sync Sidebar selection
					for i, item := range m.Sidebar.Items {
						if item.ShortName == selected.ShortName {
							m.Sidebar.Cursor = i
							break
						}
					}

					// Switch Context
					m.Sidebar.Active = false

					// Get or initialize service lazily
					svc, err := m.getOrInitializeService(context.Background(), m.ActiveService)
					if err != nil {
						cmds = append(cmds, func() tea.Msg {
							return core.StatusMsg{Message: "Failed to initialize service: " + err.Error(), IsError: true}
						})
					} else if svc != nil {
						svc.Reset() // Reset state (fix Bug 2)
						m.CurrentSvc = svc

						// Sync Window Size immediately implementation (Fix Bug: Truncated list on entry)
						if m.Width > 0 && m.Height > 0 {
							newModel, _ := svc.Update(tea.WindowSizeMsg{
								Width:  m.Width,
								Height: m.Height,
							})
							if updatedSvc, ok := newModel.(services.Service); ok {
								svc = updatedSvc
								m.ServiceMap[m.ActiveService] = svc
								m.CurrentSvc = svc // Update current pointer too
							}
						}

						// Trigger Refresh
						cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
					}
					m.setFocus(FocusMain)
				}
				return m, tea.Batch(cmds...)
			}

			// Update Home Menu
			m.HomeMenu, cmd = m.HomeMenu.Update(msg)
			return m, cmd
		}

		// SERVICE MODE
		if m.ViewMode == ViewService {
			// Handle 'q' explicitly for hierarchy navigation (Fix Bug 1)
			if msg.String() == "q" && !m.ShowHelp && m.Focus != FocusPalette {
				if m.CurrentSvc != nil && m.CurrentSvc.IsRootView() {
					// If at root of service, go back to Home
					m.ViewMode = ViewHome
					m.Sidebar.Active = false
					m.HomeMenu.IsFocused = true
					m.setFocus(FocusSidebar)
					return m, nil
				}
				// If not at root (e.g. detailed view), pass 'q' to service
				// to let it handle "back" logic
			}

			// Focus Switching - Handle BEFORE forwarding to service
			if m.Focus != FocusPalette && !m.ShowHelp {
				switch msg.String() {
				case "left":
					// Always allow escaping to sidebar with Left Arrow
					if m.Focus == FocusMain {
						m.setFocus(FocusSidebar)
						m.Sidebar.Active = true
						// Do not forward 'left' to service
						return m, nil
					}
				case "h":
					// 'h' is intentionally a no-op while in the sidebar (there is
					// nothing further left to collapse to); in FocusMain it is
					// reserved for SSH, so it is not forwarded there either.
				case "right", "l":
					if m.Focus == FocusSidebar && m.Sidebar.Visible {
						m.setFocus(FocusMain)
						m.Sidebar.Active = false
						return m, nil
					}
				case "enter":
					// 'enter' in sidebar also moves to main
					if m.Focus == FocusSidebar && m.Sidebar.Visible {
						m.setFocus(FocusMain)
						m.Sidebar.Active = false
						return m, nil
					}
				}
			}

			// If Focus is Main and we have an active service, forward keys
			if m.Focus == FocusMain && m.CurrentSvc != nil {
				var newModel tea.Model
				newModel, cmd = m.CurrentSvc.Update(msg)
				if updatedSvc, ok := newModel.(services.Service); ok {
					m.CurrentSvc = updatedSvc
					m.ServiceMap[m.CurrentSvc.ShortName()] = updatedSvc
				}
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}

	case []core.Project:
		// Projects Fetched
		var cmds []core.Command
		for _, p := range msg {
			// Capture variable
			p := p
			cmds = append(cmds, core.Command{
				Name:        p.ID,
				Description: p.Name,
				Action: func() core.Route {
					return core.Route{
						View: core.ViewHome,
						ID:   "SWITCH_PROJECT:" + p.ID,
					}
				},
			})
		}
		m.Navigation.SetCommands(cmds)
		m.StatusBar.Message = "Select a project to switch..."
		return m, nil

	case core.SwitchToLogsMsg:
		// Switch to Logging Service
		m.ViewMode = ViewService
		m.ActiveService = "logs"
		// Sync Sidebar
		for i, item := range m.Sidebar.Items {
			if item.ShortName == "logs" {
				m.Sidebar.Cursor = i
				break
			}
		}

		svc, err := m.getOrInitializeService(context.Background(), "logs")
		if err != nil {
			cmds = append(cmds, func() tea.Msg {
				return core.StatusMsg{Message: "Failed to initialize logging service: " + err.Error(), IsError: true}
			})
			// Still allow switching? m.CurrentSvc would be nil or stale.
			// Better to alert user and not switch context if fatal?
			// But getOrInitializeService returns original svc if it existed.
		}

		if svc != nil {
			m.CurrentSvc = svc
			m.ServiceMap["logs"] = svc // Ensure map is up to date

			svc.Reset()
			// Cast to Logging Service to set filter
			// We need a way to pass filter. Is it exposed?
			// The interface Service doesn't have SetFilter.
			// We can use type assertion.
			if logSvc, ok := svc.(interface{ SetFilter(string) }); ok {
				logSvc.SetFilter(msg.Filter)
			}
			if logSvc, ok := svc.(interface{ SetReturnTo(string) }); ok {
				logSvc.SetReturnTo(msg.Source)
			}
			if logSvc, ok := svc.(interface{ SetHeading(string) }); ok {
				logSvc.SetHeading(msg.Heading)
			}

			svc.Focus()

			// Sync Window Size
			if m.Width > 0 && m.Height > 0 {
				availWidth := m.Width
				// We force sidebar visible below, so account for it now
				availWidth -= m.Sidebar.Width

				newModel, _ := svc.Update(tea.WindowSizeMsg{
					Width:  availWidth,
					Height: m.Height,
				})
				if updatedSvc, ok := newModel.(services.Service); ok {
					svc = updatedSvc
					m.ServiceMap["logs"] = svc
					m.CurrentSvc = svc
				}
			}

			// Trigger Refresh
			cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
		}
		m.Focus = FocusMain
		m.Sidebar.Active = false
		m.Sidebar.Visible = true
		return m, tea.Batch(cmds...)

	case core.SwitchToServiceMsg:
		// Switch to a specific service
		m.ViewMode = ViewService
		m.ActiveService = msg.Service
		// Sync Sidebar
		for i, item := range m.Sidebar.Items {
			if item.ShortName == msg.Service {
				m.Sidebar.Cursor = i
				break
			}
		}

		if svc, exists := m.ServiceMap[msg.Service]; exists {
			svc.Reset()
			svc.Blur() // Focus sidebar initially? or Focus service?
			// If returning from logs, maybe focus service directly?
			// Let's stick to standard flow: Focus Sidebar active.
			// But if user pressed Esc in logs, they expect to be back in the list, possibly focused on list?
			// For now, consistent behavior: Sidebar active.
			m.CurrentSvc = svc

			// Sync Window Size
			if m.Width > 0 && m.Height > 0 {
				availWidth := m.Width
				if m.Sidebar.Visible {
					availWidth -= m.Sidebar.Width
				}
				newModel, _ := svc.Update(tea.WindowSizeMsg{
					Width:  availWidth, // Use available width
					Height: m.Height,
				})
				if updatedSvc, ok := newModel.(services.Service); ok {
					svc = updatedSvc
					m.ServiceMap[msg.Service] = svc
					m.CurrentSvc = svc
				}
			}

			// Trigger Refresh?
			cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
		}
		m.Focus = FocusSidebar
		m.Sidebar.Active = true
		return m, tea.Batch(cmds...)

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		components.SetGlobalSize(msg.Width, msg.Height)

		availableHeight := msg.Height - 1
		m.Sidebar.Height = availableHeight
		m.StatusBar.Width = msg.Width

		// Update home menu with screen dimensions
		m.HomeMenu.ScreenWidth = msg.Width
		m.HomeMenu.ScreenHeight = msg.Height
		m.HomeMenu.UpdateViewportRows()
		m.HomeMenu.UpdateViewportCols()

	case tea.MouseMsg:
		// Handle mouse clicks for focus switching and selection
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft {
			if m.ViewMode == ViewService && m.Sidebar.Visible {
				// Check if click is in sidebar region (left side)
				if msg.X < m.Sidebar.Width {
					// Click in sidebar - switch focus and forward event
					m.setFocus(FocusSidebar)
					m.Sidebar.Active = true
					if m.CurrentSvc != nil {
						m.CurrentSvc.Blur()
					}
					m.Sidebar, cmd = m.Sidebar.Update(msg)
					cmds = append(cmds, cmd)
				} else {
					// Click in main content area
					m.setFocus(FocusMain)
					m.Sidebar.Active = false
					if m.CurrentSvc != nil {
						m.CurrentSvc.Focus()
						// Adjust X coordinate to be relative to main content area
						adjustedMsg := tea.MouseMsg{
							X:      msg.X - m.Sidebar.Width,
							Y:      msg.Y,
							Button: msg.Button,
							Action: msg.Action,
						}
						newModel, svcCmd := m.CurrentSvc.Update(adjustedMsg)
						if updatedSvc, ok := newModel.(services.Service); ok {
							m.CurrentSvc = updatedSvc
							m.ServiceMap[m.ActiveService] = updatedSvc
						}
						cmds = append(cmds, svcCmd)
					}
				}
				return m, tea.Batch(cmds...)
			} else if m.ViewMode == ViewHome {
				// Forward to home menu - it handles its own click mapping
				m.HomeMenu, cmd = m.HomeMenu.Update(msg)
				cmds = append(cmds, cmd)
				return m, tea.Batch(cmds...)
			}
		}
		// For non-click mouse events (motion, scroll), forward to focused component
		if m.ViewMode == ViewService {
			if m.Focus == FocusSidebar {
				m.Sidebar, cmd = m.Sidebar.Update(msg)
				cmds = append(cmds, cmd)
			} else if m.CurrentSvc != nil {
				newModel, svcCmd := m.CurrentSvc.Update(msg)
				if updatedSvc, ok := newModel.(services.Service); ok {
					m.CurrentSvc = updatedSvc
				}
				cmds = append(cmds, svcCmd)
			}
		}
	}

	// Global Updates
	if m.ShowHelp {
		return m, nil
	}

	// Service Mode Specific Updates
	if m.ViewMode == ViewService {
		// Update Sidebar
		m.Sidebar, cmd = m.Sidebar.Update(msg)
		cmds = append(cmds, cmd)

		// Check for Sidebar Selection Changes
		if m.Sidebar.Active {
			selectedSvc := m.Sidebar.SelectedService()
			if selectedSvc.ShortName != "" && m.ActiveService != selectedSvc.ShortName {
				m.ActiveService = selectedSvc.ShortName
				// Get or initialize service lazily
				svc, err := m.getOrInitializeService(context.Background(), m.ActiveService)
				if err != nil {
					cmds = append(cmds, func() tea.Msg {
						return core.StatusMsg{Message: "Failed to initialize service: " + err.Error(), IsError: true}
					})
					m.CurrentSvc = nil
				} else if svc != nil {
					svc.Reset() // Reset state (fix Bug 2)

					// Sync Window Size immediately so table renders correctly
					if m.Width > 0 && m.Height > 0 {
						availWidth := m.Width
						if m.Sidebar.Visible {
							availWidth -= m.Sidebar.Width
						}
						newModel, _ := svc.Update(tea.WindowSizeMsg{
							Width:  availWidth,
							Height: m.Height,
						})
						if updatedSvc, ok := newModel.(services.Service); ok {
							svc = updatedSvc
							m.ServiceMap[m.ActiveService] = svc
						}
					}

					m.CurrentSvc = svc
					m.setFocus(m.Focus)
					// Trigger Refresh
					cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
				} else {
					m.CurrentSvc = nil
				}
			}
		}

		// Update Active Service (if it has background work)
		if m.CurrentSvc != nil {
			// A real terminal resize carries the full terminal width; adjust
			// it for the sidebar before forwarding, same as every other path
			// that syncs window size to a service.
			forwardMsg := msg
			if wsMsg, ok := msg.(tea.WindowSizeMsg); ok {
				availWidth := wsMsg.Width
				if m.Sidebar.Visible {
					availWidth -= m.Sidebar.Width
				}
				forwardMsg = tea.WindowSizeMsg{Width: availWidth, Height: wsMsg.Height}
			}

			var newModel tea.Model
			newModel, cmd = m.CurrentSvc.Update(forwardMsg)
			if updatedSvc, ok := newModel.(services.Service); ok {
				m.CurrentSvc = updatedSvc
				m.ServiceMap[m.CurrentSvc.ShortName()] = updatedSvc
			}
			cmds = append(cmds, cmd)
		}
	}

	// Update StatusBar
	m.StatusBar, cmd = m.StatusBar.Update(msg)
	cmds = append(cmds, cmd)

	// Dynamic Help Text
	if m.Focus == FocusPalette {
		m.StatusBar.SetHelpText("Esc:Cancel  Enter:Run  ↑/↓:Select")
	} else if m.ViewMode == ViewHome {
		// Add help hint if help is not currently shown
		helpText := "q:Quit  Enter:Select"
		if !m.ShowHelp {
			helpText += "  ?:Help"
		}
		m.StatusBar.SetHelpText(helpText)
	} else if m.CurrentSvc != nil {
		// Get service help text and append help hint if help is not currently shown
		helpText := m.CurrentSvc.HelpText()
		if !m.ShowHelp {
			// Append help hint to service help text
			if helpText != "" {
				helpText += "  ?:Help"
			} else {
				helpText = "?:Help"
			}
		}
		m.StatusBar.SetHelpText(helpText)
	} else {
		// Fallback: show help hint if available
		if !m.ShowHelp {
			m.StatusBar.SetHelpText("?:Help")
		} else {
			m.StatusBar.SetHelpText("")
		}
	}

	return m, tea.Batch(cmds...)
}

// View renders the current UI based on state
// See home.go for the actual view logic

// registerAllServices registers all available services with the registry
// This is kept in the ui package to avoid import cycles (services import core, core shouldn't import services)
func registerAllServices(registry *core.ServiceRegistry) {
	registry.Register("overview", func(cache *core.Cache) services.Service {
		return overview.NewService(cache)
	})
	registry.Register("gce", func(cache *core.Cache) services.Service {
		return gce.NewService(cache)
	})
	registry.Register("gke", func(cache *core.Cache) services.Service {
		return gke.NewService(cache)
	})
	registry.Register("disks", func(cache *core.Cache) services.Service {
		return disks.NewService(cache)
	})
	registry.Register("filestore", func(cache *core.Cache) services.Service {
		return filestore.NewService(cache)
	})
	registry.Register("pubsub", func(cache *core.Cache) services.Service {
		return pubsub.NewService(cache)
	})
	registry.Register("scheduler", func(cache *core.Cache) services.Service {
		return scheduler.NewService(cache)
	})
	registry.Register("cloudtasks", func(cache *core.Cache) services.Service {
		return cloudtasks.NewService(cache)
	})
	registry.Register("redis", func(cache *core.Cache) services.Service {
		return redis.NewService(cache)
	})
	registry.Register("spanner", func(cache *core.Cache) services.Service {
		return spanner.NewService(cache)
	})
	registry.Register("bigtable", func(cache *core.Cache) services.Service {
		return bigtable.NewService(cache)
	})
	registry.Register("dataflow", func(cache *core.Cache) services.Service {
		return dataflow.NewService(cache)
	})
	registry.Register("dataproc", func(cache *core.Cache) services.Service {
		return dataproc.NewService(cache)
	})
	registry.Register("firestore", func(cache *core.Cache) services.Service {
		return firestore.NewService(cache)
	})
	registry.Register("sql", func(cache *core.Cache) services.Service {
		return cloudsql.NewService(cache)
	})
	registry.Register("iam", func(cache *core.Cache) services.Service {
		return iam.NewService(cache)
	})
	registry.Register("run", func(cache *core.Cache) services.Service {
		return cloudrun.NewService(cache)
	})
	registry.Register("gcs", func(cache *core.Cache) services.Service {
		return gcs.NewService(cache)
	})
	registry.Register("bq", func(cache *core.Cache) services.Service {
		return bigquery.NewService(cache)
	})
	registry.Register("net", func(cache *core.Cache) services.Service {
		return net.NewService(cache)
	})
	registry.Register("ipaddress", func(cache *core.Cache) services.Service {
		return ipaddress.NewService(cache)
	})
	registry.Register("loadbalancing", func(cache *core.Cache) services.Service {
		return loadbalancing.NewService(cache)
	})
	registry.Register("dns", func(cache *core.Cache) services.Service {
		return dns.NewService(cache)
	})
	registry.Register("kms", func(cache *core.Cache) services.Service {
		return kms.NewService(cache)
	})
	registry.Register("functions", func(cache *core.Cache) services.Service {
		return cloudfunctions.NewService(cache)
	})
	registry.Register("logs", func(cache *core.Cache) services.Service {
		return logging.NewService(cache)
	})
	registry.Register("monitoring", func(cache *core.Cache) services.Service {
		return monitoring.NewService(cache)
	})
	registry.Register("secrets", func(cache *core.Cache) services.Service {
		return secrets.NewService(cache)
	})
	registry.Register("parametermanager", func(cache *core.Cache) services.Service {
		return parametermanager.NewService(cache)
	})
	registry.Register("cloudbuild", func(cache *core.Cache) services.Service {
		return cloudbuild.NewService(cache)
	})
	registry.Register("artifactregistry", func(cache *core.Cache) services.Service {
		return artifactregistry.NewService(cache)
	})
	registry.Register("jobs", func(cache *core.Cache) services.Service {
		return jobs.NewService(cache)
	})
}

// tabbedService is implemented by services with more than one tab (see
// serviceSubTabs below). SetActiveTab returns false for an unrecognized key
// (callers treat that as a harmless no-op rather than an error, since a
// mistyped or stale key shouldn't break navigation), plus a tea.Cmd that
// MUST be run when non-nil -- switching to a tab whose data hasn't been
// fetched yet needs the same fetch the service's own '['/']' handling
// triggers (e.g. GCE's Instance Groups, Cloud Run's Functions, every
// Load Balancing tab past the default); forgetting this leaves the target
// tab's table permanently empty since nothing else will ever populate it.
type tabbedService interface {
	SetActiveTab(tab string) (bool, tea.Cmd)
}

// subServiceTab names one non-default tab of a tabbed service, so it can get
// its own independently-searchable/selectable palette command (e.g.
// "VPC Network: Firewall Rules") instead of only being reachable by first
// opening the service and then pressing '['/']'/Tab blind. Key is passed to
// the service's SetActiveTab; Label is appended to "<service name>: " to
// form the command's Name.
//
// Each service's own default tab (whatever Reset() sets activeTab to) is
// deliberately NOT listed here -- the service's own top-level command
// already opens on that tab, so adding a redundant "Foo: Default Tab" entry
// would just be a near-duplicate of "Foo" with no new capability.
type subServiceTab struct {
	Key   string
	Label string
}

// serviceSubTabs lists the extra (non-default) tabs per service short name.
// Keys here must match the case strings each service's own SetActiveTab
// switches on (internal/services/{gce,net,monitoring,loadbalancing,cloudrun,pubsub}).
var serviceSubTabs = map[string][]subServiceTab{
	"gce":        {{Key: "instance-groups", Label: "Instance Groups (MIGs)"}},
	"net":        {{Key: "firewalls", Label: "Firewall Rules"}},
	"monitoring": {{Key: "alert-policies", Label: "Alert Policies"}},
	"loadbalancing": {
		{Key: "health-checks", Label: "Health Checks"},
		{Key: "url-maps", Label: "URL Maps"},
		{Key: "forwarding-rules", Label: "Forwarding Rules"},
		{Key: "ssl-certificates", Label: "SSL Certificates"},
	},
	"run":    {{Key: "functions", Label: "Functions"}},
	"pubsub": {{Key: "subscriptions", Label: "Subscriptions"}},
}

// serviceCommands builds one command-palette entry per registered service,
// derived directly from the service registry rather than a hand-maintained
// list — a hardcoded list previously drifted out of sync as new services
// were added, silently leaving them unreachable from the ":" palette. Sorted
// alphabetically by display name for a stable, predictable order.
func serviceCommands(svcMap map[string]services.Service) []core.Command {
	cmds := make([]core.Command, 0, len(svcMap))
	for shortName, svc := range svcMap {
		name := svc.Name()
		short := shortName
		cmds = append(cmds, core.Command{
			Name:        name,
			Description: fmt.Sprintf("Open %s (%s)", name, short),
			Action: func() core.Route {
				return core.Route{View: core.ViewServiceList, Service: short}
			},
		})

		for _, sub := range serviceSubTabs[short] {
			sub := sub
			cmds = append(cmds, core.Command{
				Name:        fmt.Sprintf("%s: %s", name, sub.Label),
				Description: fmt.Sprintf("Open %s under %s (%s)", sub.Label, name, short),
				Action: func() core.Route {
					return core.Route{View: core.ViewServiceList, Service: short, SubTab: sub.Key}
				},
			})
		}
	}
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].Name < cmds[j].Name })
	return cmds
}

func (m *MainModel) setFocus(area FocusArea) {
	m.Focus = area
	if m.ViewMode == ViewService && m.CurrentSvc != nil {
		switch area {
		case FocusMain:
			m.CurrentSvc.Focus()
		case FocusSidebar:
			m.CurrentSvc.Blur()
		}
	}
	m.syncStatusBarFocus()
}

func (m *MainModel) syncStatusBarFocus() {
	if m.ViewMode == ViewHome {
		m.StatusBar.SetFocusPane("HOME")
		return
	}

	switch m.Focus {
	case FocusSidebar:
		m.StatusBar.SetFocusPane("SIDEBAR")
	case FocusMain:
		m.StatusBar.SetFocusPane("MAIN")
	default:
		m.StatusBar.SetFocusPane("")
	}
}
