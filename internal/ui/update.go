package ui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/services"
	"github.com/yogirk/tgcp/internal/ui/components"
	"github.com/yogirk/tgcp/internal/services/overview"
)

// Update handles messages and updates the model
func (m MainModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd
	defer m.syncStatusBarFocus()

	switch msg := msg.(type) {
	// Toast Notifications -- shown in the status bar rather than an overlay.
	case core.ToastMsg:
		now := time.Now()
		m.statusMsgSetAt = now
		m.StatusBar.Message = msg.Message
		m.StatusBar.IsError = msg.Type == core.ToastError
		duration := msg.Duration
		if duration == 0 {
			duration = 3 * time.Second
		}
		return m, tea.Tick(duration, func(time.Time) tea.Msg {
			return statusMsgExpiredMsg{at: now}
		})

	case statusMsgExpiredMsg:
		if m.statusMsgSetAt.Equal(msg.at) {
			m.StatusBar.Message = "Ready"
			m.StatusBar.IsError = false
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
		var handled bool
		m, cmd, handled = m.handleKeyMsg(msg)
		if handled {
			return m, cmd
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case []core.Project:
		// Projects Fetched
		var navCmds []core.Command
		for _, p := range msg {
			p := p
			navCmds = append(navCmds, core.Command{
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
		m.Navigation.SetCommands(navCmds)
		m.StatusBar.Message = "Select a project to switch..."
		return m, nil

	case core.SwitchToLogsMsg:
		var c []tea.Cmd
		m, c = m.handleSwitchToLogs(msg)
		cmds = append(cmds, c...)
		return m, tea.Batch(cmds...)

	case core.SwitchToServiceMsg:
		var c []tea.Cmd
		m, c = m.handleSwitchToService(msg)
		cmds = append(cmds, c...)
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
		var handled bool
		var c []tea.Cmd
		m, c, handled = m.handleMouseMsg(msg)
		if handled {
			cmds = append(cmds, c...)
			return m, tea.Batch(cmds...)
		}
		cmds = append(cmds, c...)
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

	return m, tea.Batch(cmds...)
}

func (m *MainModel) handleKeyMsg(msg tea.KeyMsg) (MainModel, tea.Cmd, bool) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	// Global Keybindings
	if m.Focus != FocusPalette {
		switch msg.String() {
		case "q", "esc":
			if m.ShowHelp {
				m.ShowHelp = false
				return *m, nil, true
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
				return *m, tea.Quit, true
			}
			// If in Service Mode, we delegate to the service specific block below
			// or if IsRootView logic there handles it.
			// But wait, if we are in Service Mode and IsRootView is true, we want Home.
			// The block below handles it. But if we are here, we need to NOT Quit.
			// Effectively, if ViewService, don't do anything here, let next block handle.
			if m.ViewMode == ViewService {
				// Fallthrough to Service Loop
			} else {
				return *m, tea.Quit, true
			}
		case "ctrl+c":
			return *m, tea.Quit, true
		case "ctrl+l":
			// Manual force-redraw: some terminal multiplexers can desync
			// their own screen buffer from bubbletea's diffed output
			// under bursts of rapid input, leaving stale content on
			// screen. Ctrl+L is the standard terminal convention for
			// "redraw" and forces a full repaint to recover.
			return *m, tea.ClearScreen, true
		case ":":
			if m.ViewMode == ViewHome && m.HomeMenu.FilterActive() {
				break // let filter input receive ":"
			}
			m.LastFocus = m.Focus
			m.setFocus(FocusPalette)
			m.Navigation.PaletteActive = true
			m.StatusBar.Mode = "COMMAND"
			m.StatusBar.Message = "Type command..."
			return *m, nil, true
		case "?":
			if m.ViewMode == ViewHome && m.HomeMenu.FilterActive() {
				break // let filter input receive "?"
			}
			m.ShowHelp = !m.ShowHelp
			return *m, nil, true
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
			return *m, nil, true
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
					return *m, svcCmd, true
				}
			}
			return *m, nil, true
		case "tab", "shift+tab":
			// Cycle the active service's own tabs (e.g. Cloud Run's
			// Services/Functions, Load Balancing's 5 resource tabs) by
			// calling the service directly via TabCycler, rather than
			// re-injecting a synthetic "]"/"[" KeyMsg through Update --
			// a fake keystroke could be misdelivered to (or mistaken
			// for) real input elsewhere in the service (e.g. a filter
			// box or query input focused at what still counts as the
			// service's "root" view), exactly as a real Tab used to be
			// misinterpreted as "]" while Cloud Logging's query filter
			// was focused. The service itself now decides, from its own
			// state, whether Tab means "cycle tabs" right now; if not
			// (ok == false), the real Tab/Shift+Tab key falls through
			// to the normal service-forwarding path below instead (e.g.
			// components.FormModel's own "next field" handling).
			if m.ViewMode == ViewService && m.CurrentSvc != nil {
				if tc, ok := m.CurrentSvc.(services.TabCycler); ok {
					var svcCmd tea.Cmd
					var handled bool
					if msg.String() == "shift+tab" {
						svcCmd, handled = tc.PrevTab()
					} else {
						svcCmd, handled = tc.NextTab()
					}
					if handled {
						return *m, svcCmd, true
					}
				}
			}
		}
	} else {
		// Palette specific keys (Esc to close)
		switch msg.String() {
		case "esc":
			m.setFocus(m.LastFocus)
			m.Navigation.PaletteActive = false
			m.StatusBar.Message = "Ready"
			m.Palette.TextInput.Reset()        // Clear input
			m.Navigation.RestoreBaseCommands() // Reset to default commands
			m.Navigation.FilterCommands("")
			return *m, nil, true
		case "up", "ctrl+p":
			m.Navigation.SelectPrev()
			return *m, nil, true
		case "down", "ctrl+n":
			m.Navigation.SelectNext()
			return *m, nil, true
		case "enter":
			// Execute Command
			if route := m.Navigation.ExecuteSelection(); route != nil {
				c := m.handlePaletteRoute(route)
				cmds = append(cmds, c...)
			}
			return *m, tea.Batch(cmds...), true
		}

		// Forward other keys to Palette Input
		m.Palette, cmd = m.Palette.Update(msg)
		cmds = append(cmds, cmd)

		// Update Suggestions
		if m.Palette.TextInput.Value() != m.Navigation.Query {
			m.Navigation.FilterCommands(m.Palette.TextInput.Value())
		}

		return *m, tea.Batch(cmds...), true
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
			return *m, tea.Batch(cmds...), true
		}

		// Update Home Menu
		m.HomeMenu, cmd = m.HomeMenu.Update(msg)
		return *m, cmd, true
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
				return *m, nil, true
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
					return *m, nil, true
				}
			case "h":
				// 'h' is intentionally a no-op while in the sidebar (there is
				// nothing further left to collapse to); in FocusMain it is
				// reserved for SSH, so it is not forwarded there either.
			case "right", "l":
				if m.Focus == FocusSidebar && m.Sidebar.Visible {
					m.setFocus(FocusMain)
					m.Sidebar.Active = false
					return *m, nil, true
				}
			case "enter":
				// 'enter' in sidebar also moves to main
				if m.Focus == FocusSidebar && m.Sidebar.Visible {
					m.setFocus(FocusMain)
					m.Sidebar.Active = false
					return *m, nil, true
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
			return *m, tea.Batch(cmds...), true
		}
	}

	return *m, tea.Batch(cmds...), false
}

func (m *MainModel) handlePaletteRoute(route *core.Route) []tea.Cmd {
	var cmds []tea.Cmd
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
		return cmds
	case core.ViewQuickProjectSwitcher:
		// No API call needed -- list is synchronous, from config.
		m.showConfiguredProjectSwitcher()
		m.setFocus(FocusPalette)
		return cmds
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
	return cmds
}

func (m *MainModel) handleSwitchToLogs(msg core.SwitchToLogsMsg) (MainModel, []tea.Cmd) {
	var cmds []tea.Cmd
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
		if logSvc, ok := svc.(interface{ SetLive(bool) }); ok {
			logSvc.SetLive(msg.Live)
		}

		svc.Focus()

		// Sync Window Size
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
				m.ServiceMap["logs"] = svc
				m.CurrentSvc = svc
			}
		}

		// Trigger Refresh
		cmds = append(cmds, func() tea.Msg { return svc.Refresh()() })
	}
	m.Focus = FocusMain
	m.Sidebar.Active = false
	// Respect whatever visibility the user already had (matches
	// SwitchToServiceMsg below) -- this used to force the sidebar open
	// unconditionally, which re-opened it every time even if the user had
	// explicitly hidden it before jumping to logs.
	return *m, cmds
}

func (m *MainModel) handleSwitchToService(msg core.SwitchToServiceMsg) (MainModel, []tea.Cmd) {
	var cmds []tea.Cmd
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
	return *m, cmds
}

func (m *MainModel) handleMouseMsg(msg tea.MouseMsg) (MainModel, []tea.Cmd, bool) {
	var cmd tea.Cmd
	var cmds []tea.Cmd
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
			return *m, cmds, true
		} else if m.ViewMode == ViewHome {
			// Forward to home menu - it handles its own click mapping
			m.HomeMenu, cmd = m.HomeMenu.Update(msg)
			cmds = append(cmds, cmd)
			return *m, cmds, true
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
	return *m, cmds, false
}
