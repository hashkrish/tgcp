package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/services/overview"
	"github.com/yogirk/tgcp/internal/styles"
)

// View renders the application UI
func (m MainModel) View() string {
	// 1. Check for Help Overlay
	if m.ShowHelp {
		return HelpView(m.Width, m.Height)
	}

	// 2. Check for Start-up Error (Auth)
	if !m.AuthState.Authenticated {
		return renderAuthError(m)
	}

	var content string

	if m.ViewMode == ViewHome {
		content = renderLandingPage(m)
	} else {
		content = renderServiceLayout(m)
	}

	// Status Bar (Always Visible at bottom)
	statusBar := m.StatusBar.View()

	// Layout Content + Status Bar
	screen := lipgloss.JoinVertical(lipgloss.Top, content, statusBar)

	// 3. Toast Overlay (if active)
	if m.Toast != nil && !m.Toast.IsExpired() {
		toastView := m.Toast.View()
		// Position toast at bottom-right, above status bar
		screen = lipgloss.JoinVertical(lipgloss.Top,
			content,
			lipgloss.PlaceHorizontal(m.Width, lipgloss.Right, toastView),
			statusBar,
		)
	}

	// 4. Loading Spinner (if active) - show inline at top of content
	if m.Spinner.IsActive() {
		spinnerView := m.Spinner.View()
		screen = lipgloss.JoinVertical(lipgloss.Top, spinnerView, content, statusBar)
	}

	// 5. Overlays (Command Palette)
	if m.Navigation.PaletteActive {
		// Overlay Palette on top of the entire screen
		// Note: Palette.Render uses lipgloss.Place to center itself in the given dimensions.
		// otherRows is a conservative estimate of the palette's own chrome
		// (input box + spacers + help hint; the variable-height suggestions
		// list is deliberately not counted here, since palette.Render has
		// its own follow-up degrade step -- dropping the help hint -- for
		// whatever doesn't fit once the real suggestion count is known).
		const paletteOtherRows = 6
		banner := chooseBanner(m.Height-1, paletteOtherRows)
		paletteView := m.Palette.Render(m.Navigation, m.Width, m.Height, banner)

		// To truly "overlay" in TUI without clearing background is hard with just string concatenation.
		// However, lipgloss.Place will fill the screen with whitespace if we aren't careful.
		// A common trick is to just return only the palette view if we want it modal?
		// No, we want transparency or at least context.
		// But in simple TUI, rendering a "modal" often means rendering it *instead* of content, or
		// rendering it on top if the terminal supports cursor positioning hacks, but bubbletea 'View' returns a string.
		//
		// If we return just paletteView (which is centered), the rest is blank?
		// Palette.Render does `lipgloss.Place(..., ui)`. If whitespace is handled, it replaces screen.
		// Let's try just returning the palette view for focus, as it's a modal task.
		// It's cleaner than trying to merge strings.
		return paletteView
	}

	return screen
}

// buildStatsLine renders a one-line resource-count summary from a lightweight
// inventory snapshot (see MainModel.fetchLandingStatsCmd in model.go). Zero
// counts are omitted rather than shown as "0 X". Returns "" if inv is nil or
// every count is zero, so the caller can treat an empty string as "nothing
// to show" uniformly.
func buildStatsLine(inv *overview.ResourceInventory) string {
	if inv == nil {
		return ""
	}
	var parts []string
	add := func(icon string, count int, label string) {
		if count <= 0 {
			return
		}
		if count != 1 {
			label += "s"
		}
		parts = append(parts, fmt.Sprintf("%s %d %s", icon, count, label))
	}
	add("🖥", inv.InstanceCount, "VM")
	add("💾", inv.DiskCount, "Disk")
	add("🪣", inv.BucketCount, "Bucket")
	add("🗄", inv.SQLCount, "SQL instance")
	add("📊", inv.DatasetCount, "Dataset")
	if len(parts) == 0 {
		return ""
	}
	return styles.SubtleStyle.Render(strings.Join(parts, "   ·   "))
}

// renderLandingPage renders the central home screen
func renderLandingPage(m MainModel) string {

	// User Info Box
	email := m.AuthState.UserEmail
	if strings.Contains(email, "@") {
		parts := strings.Split(email, "@")
		if len(parts[0]) > 2 {
			email = parts[0][:2] + "***@" + parts[1]
		} else {
			email = "***@" + parts[1]
		}
	}
	// Muted inline info line — lets the banner and the service menu breathe
	// without a second rounded box stacking right below the banner.
	userInfo := styles.SubtleStyle.Render(fmt.Sprintf(
		"👤 %s   ·   📁 %s",
		email,
		m.AuthState.ProjectID,
	))

	// Stats strip (lightweight resource-count snapshot; see
	// MainModel.fetchLandingStatsCmd). Empty until the one-shot background
	// fetch completes, or if every count comes back zero.
	statsLine := buildStatsLine(m.LandingStats)
	showStats := statsLine != ""

	// Menu
	menu := m.HomeMenu.View()

	// Minimal navigation hint
	hints := styles.SubtleStyle.Render("↑/↓ navigate   / filter   Enter select   ? help   : palette")

	// Version info
	versionText := styles.SubtleStyle.Render(m.Version.FormatVersion())

	// Update notification (if available)
	var updateNotice string
	if m.UpdateInfo != nil && m.UpdateInfo.Available {
		updateStyle := lipgloss.NewStyle().
			Foreground(styles.ColorSuccess).
			Bold(true)
		updateNotice = updateStyle.Render(
			fmt.Sprintf("Update available: %s -> %s", m.Version.FormatVersion(), "v"+m.UpdateInfo.LatestVersion),
		) + "\n" + styles.SubtleStyle.Render("Run: brew upgrade tgcp  or  visit github.com/yogirk/tgcp/releases")
	}

	// Available rows for the whole body (status bar takes the last row).
	// If the full layout doesn't fit, bubbletea's renderer falls back to
	// silently cropping lines off the TOP of every frame to make it fit —
	// which is fragile (it depends on every frame computing an identical
	// height, and misbehaves under terminal emulators/multiplexers that
	// don't handle a redraw taller than the screen cleanly). So instead we
	// degrade the layout ourselves, in a fixed, non-jittery order, until it
	// actually fits within the terminal.
	available := m.Height - 1

	menuHeight := lipgloss.Height(menu)
	// userInfo(1) + gap(1) + menu + gap(1) + hints(1) + version(1)
	baseChromeRows := 1 + 1 + menuHeight + 1 + 1 + 1
	chromeRows := baseChromeRows
	if showStats {
		chromeRows += 2 // stats line + its gap
	}

	// Degrade order: the stats strip is the newest, least essential
	// element, so it's dropped first -- before the banner/version/hints
	// steps below get a chance to fire -- rather than competing with them
	// on equal footing.
	if showStats && BannerHeight+1+chromeRows > available {
		showStats = false
		chromeRows = baseChromeRows
	}

	banner := chooseBanner(available, chromeRows+1)
	bannerHeight := lipgloss.Height(banner)

	showVersion := true
	showHints := true
	if bannerHeight+1+chromeRows > available {
		// Still too tall on a very short terminal: drop the version line,
		// then the hint line, before ever falling back to cropping.
		showVersion = false
		chromeRows--
	}
	if bannerHeight+1+chromeRows > available {
		showHints = false
	}

	// Combine components vertically. NOTE: a gap must be "" here, not "\n" —
	// lipgloss.JoinVertical splits each element on "\n" to get its lines, and
	// splitting the single-character string "\n" yields TWO empty lines
	// (["", ""]), silently doubling every gap. "" splits to a single [""].
	bodyParts := []string{
		banner,
		"",
		userInfo,
	}
	if showStats {
		bodyParts = append(bodyParts, "", statsLine)
	}
	bodyParts = append(bodyParts,
		"",
		menu,
	)
	if showHints || showVersion {
		bodyParts = append(bodyParts, "")
	}
	if showHints {
		bodyParts = append(bodyParts, hints)
	}
	if showVersion {
		bodyParts = append(bodyParts, versionText)
	}

	// Add update notice if available
	if updateNotice != "" {
		bodyParts = append(bodyParts, "", updateNotice)
	}

	body := lipgloss.JoinVertical(lipgloss.Center, bodyParts...)

	return lipgloss.Place(
		m.Width, available,
		lipgloss.Center, lipgloss.Center,
		body,
	)
}

// renderServiceLayout renders the sidebar + service content
func renderServiceLayout(m MainModel) string {
	// Left: Sidebar (if visible)
	leftPanel := m.Sidebar.View()

	// Right: Service View
	var rightPanel string
	if m.CurrentSvc != nil {
		rightPanel = m.CurrentSvc.View()
	} else {
		rightPanel = styles.BaseStyle.Render("Select a service from the sidebar.")
	}

	// Calculate Right Panel Width
	sidebarWidth := lipgloss.Width(leftPanel)
	rightPanelWidth := m.Width - sidebarWidth

	// Apply style to right panel to fill space
	rightPanelStyle := lipgloss.NewStyle().
		Width(rightPanelWidth).
		Height(m.Sidebar.Height).
		Padding(1)

	rightPanel = rightPanelStyle.Render(rightPanel)

	return lipgloss.JoinHorizontal(lipgloss.Top, leftPanel, rightPanel)
}
