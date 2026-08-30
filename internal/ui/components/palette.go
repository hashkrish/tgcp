package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/core"
	"github.com/yogirk/tgcp/internal/styles"
)

type PaletteModel struct {
	TextInput textinput.Model
}

func NewPalette() PaletteModel {
	ti := textinput.New()
	ti.Placeholder = "Search services, actions..."
	ti.Prompt = "➜ "
	ti.CharLimit = 50
	ti.Width = 40
	ti.Focus() // Always focused when visible

	return PaletteModel{
		TextInput: ti,
	}
}

func (m PaletteModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m PaletteModel) Update(msg tea.Msg) (PaletteModel, tea.Cmd) {
	var cmd tea.Cmd
	m.TextInput, cmd = m.TextInput.Update(msg)
	return m, cmd
}

// highlightMatches renders text with matched characters highlighted
// matchedIndexes are positions in the full "Name Description" string
// We only highlight the name portion for cleaner display
func highlightMatches(name, description string, matchedIndexes []int) (string, string) {
	// Build a set of matched indexes for O(1) lookup
	matchSet := make(map[int]bool)
	for _, idx := range matchedIndexes {
		matchSet[idx] = true
	}

	// Style for highlighted (matched) characters
	highlightStyle := lipgloss.NewStyle().
		Foreground(styles.ColorBrandAccent).
		Bold(true)

	// Style for normal characters in name
	nameStyle := lipgloss.NewStyle().
		Foreground(styles.ColorTextPrimary).
		Bold(true)

	// Build highlighted name (index by rune position, not byte offset, so
	// multi-byte characters line up with matchedIndexes from the fuzzy matcher)
	nameRunes := []rune(name)
	var nameBuilder strings.Builder
	for i, r := range nameRunes {
		char := string(r)
		if matchSet[i] {
			nameBuilder.WriteString(highlightStyle.Render(char))
		} else {
			nameBuilder.WriteString(nameStyle.Render(char))
		}
	}

	// Description uses muted style, highlight matches there too
	descStyle := styles.SubtleStyle
	var descBuilder strings.Builder
	nameLen := len(nameRunes) + 1 // +1 for the space between name and description

	for i, r := range []rune(description) {
		char := string(r)
		if matchSet[nameLen+i] {
			descBuilder.WriteString(highlightStyle.Render(char))
		} else {
			descBuilder.WriteString(descStyle.Render(char))
		}
	}

	return nameBuilder.String(), descBuilder.String()
}

// paletteVisibleCount computes how many suggestion rows fit given the
// terminal height, mirroring home_menu.go's UpdateViewportRows() row-count-
// from-height formula. overhead is a conservative estimate of everything
// else the palette renders (banner up to 6 rows, spacers, the input box,
// the help hint) so suggestions don't get squeezed off a short terminal.
func paletteVisibleCount(screenHeight int) int {
	const overhead = 12
	count := screenHeight - overhead
	if count < 4 {
		count = 4
	}
	if count > 12 {
		count = 12
	}
	return count
}

// Render renders the palette overlay using the provided navigation state
func (m PaletteModel) Render(nav core.NavigationModel, screenWidth, screenHeight int, banner string) string {
	if screenWidth <= 0 {
		screenWidth = 80
	}
	if screenHeight <= 0 {
		screenHeight = 24
	}

	// boxWidth: floor 40 (usable on a narrow terminal), ceiling 100 (raised
	// from a previous flat 72 -- a wide terminal has plenty of room for
	// longer command descriptions before the box needs to stop growing).
	boxWidth := min(max(screenWidth-6, 40), 100)

	// 1. Input Box
	// Determine if we have a dropdown (suggestions or "no matches")
	hasDropdown := len(nav.Suggestions) > 0 || m.TextInput.Value() != ""

	// Build input box style - seamless with dropdown when present.
	// Padding(0, 1): horizontal-only, matching FilterModel's convention
	// (internal/ui/components/filter.go) -- a full Padding(1) reads as
	// unnecessarily loose chrome around a single-line search input.
	var inputBoxStyle lipgloss.Style
	if hasDropdown {
		// No bottom border - connects seamlessly with dropdown
		inputBoxStyle = lipgloss.NewStyle().
			Width(boxWidth).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder(), true, true, false, true). // No bottom
			BorderForeground(styles.ColorBrandAccent)
	} else {
		// Full border when no dropdown
		inputBoxStyle = lipgloss.NewStyle().
			Width(boxWidth).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(styles.ColorBrandAccent)
	}

	inputView := inputBoxStyle.Render(m.TextInput.View())

	// 2. Suggestions List
	var suggestionsView string
	if len(nav.Suggestions) > 0 {
		total := len(nav.Suggestions)
		visibleCount := paletteVisibleCount(screenHeight)

		// Scroll window: computed fresh each render from nav.Selection alone
		// (no persisted scroll-offset state needed) so the selected row
		// always stays inside [start, start+visibleCount) -- same "follow
		// the cursor" behavior as home_menu.go's adjustScroll(), just
		// stateless since Render() already gets the full nav state every
		// frame.
		start := 0
		if total > visibleCount {
			start = nav.Selection - visibleCount + 1
			if start < 0 {
				start = 0
			}
			if maxStart := total - visibleCount; start > maxStart {
				start = maxStart
			}
		}
		end := min(start+visibleCount, total)

		var lines []string
		if start > 0 {
			lines = append(lines, styles.SubtleStyle.Render("  ↑ more"))
		}
		for i := start; i < end; i++ {
			match := nav.Suggestions[i]

			// Render Item with highlighted matches
			name, desc := highlightMatches(match.Name, match.Description, match.MatchedIndexes)

			// Layout: Name (padded) Description
			// Use lipgloss width for proper padding with ANSI codes
			nameWidth := 25
			nameRendered := lipgloss.NewStyle().Width(nameWidth).Render(name)
			content := nameRendered + " " + desc

			if i == nav.Selection {
				// Highlighted
				content = styles.SelectedActive.
					Width(boxWidth - 2). // Match box width approx (padding)
					Render(content)
			} else {
				// Normal
				content = styles.UnselectedItemStyle.
					PaddingLeft(styles.SpaceS).
					Render(content)
			}
			lines = append(lines, content)
		}
		if end < total {
			lines = append(lines, styles.SubtleStyle.Render("  ↓ more"))
		}
		suggestionsView = lipgloss.JoinVertical(lipgloss.Left, lines...)

		// Style the dropdown - no top border, same accent color as input
		suggestionsView = styles.OverlayBoxStyle.
			Width(boxWidth).
			Border(lipgloss.RoundedBorder(), false, true, true, true). // No top border
			BorderForeground(styles.ColorBrandAccent).                 // Match input border color
			Render(suggestionsView)
	} else if m.TextInput.Value() != "" {
		// No matches - still connected to input
		suggestionsView = styles.OverlayBoxStyle.
			Width(boxWidth).
			Border(lipgloss.RoundedBorder(), false, true, true, true).
			BorderForeground(styles.ColorBrandAccent). // Match input border color
			Padding(0, 1).
			Render(styles.SubtleStyle.Render("No matching commands"))
	}

	helpHint := styles.SubtleStyle.Render("Esc:Cancel  Enter:Run  ↑/↓/^P/^N:Select")

	// 3. Combine: Banner -> Buffer -> Input -> List
	// The banner is passed in (already degraded to the compact wordmark by
	// the caller if the terminal is short -- see chooseBanner in banner.go).
	//
	// NOTE: spacer elements must be "" not "\n" -- lipgloss.JoinVertical
	// splits each element on "\n" to get its lines, and splitting the
	// single-character string "\n" yields TWO empty lines (["", ""]),
	// silently doubling the gap. This is the same bug class documented and
	// fixed in home.go's landing page.
	buildUI := func(showHint bool) string {
		parts := []string{banner, "", inputView, suggestionsView, ""}
		if showHint {
			parts = append(parts, helpHint)
		}
		return lipgloss.JoinVertical(lipgloss.Center, parts...)
	}

	ui := buildUI(true)
	// If even the compact banner doesn't leave room for everything, drop the
	// help hint line before giving up -- mirrors the landing page's own
	// "drop hints when short" degrade step (home.go), scoped here to just
	// the data this component already has (no cross-package banner-choice
	// logic needed, since the caller already picked full-vs-compact banner).
	if lipgloss.Height(ui) > screenHeight {
		ui = buildUI(false)
	}

	// 4. Center in Screen without backdrop to avoid ghosting/shadows
	return lipgloss.Place(screenWidth, screenHeight,
		lipgloss.Center, lipgloss.Center,
		ui,
	)
}
