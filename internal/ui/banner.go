package ui

import (
	"github.com/charmbracelet/lipgloss"
)

var (
	// Google Colors
	colorBlue   = lipgloss.Color("#4285F4")
	colorRed    = lipgloss.Color("#DB4437")
	colorYellow = lipgloss.Color("#F4B400")
	colorGreen  = lipgloss.Color("#0F9D58")

	// Individual Letter Styles
	styleT = lipgloss.NewStyle().Foreground(colorBlue)
	styleG = lipgloss.NewStyle().Foreground(colorRed)
	styleC = lipgloss.NewStyle().Foreground(colorYellow)
	styleP = lipgloss.NewStyle().Foreground(colorGreen)
)

// GetBanner returns the colored ASCII art banner
func GetBanner() string {
	// Individual letters broken down from the original banner
	// Original:
	// ████████╗ ██████╗  ██████╗██████╗
	// ╚══██╔══╝██╔════╝ ██╔════╝██╔══██╗
	//    ██║   ██║  ███╗██║     ██████╔╝
	//    ██║   ██║   ██║██║     ██╔═══╝
	//    ██║   ╚██████╔╝╚██████╗██║
	//    ╚═╝    ╚═════╝  ╚═════╝╚═╝

	letterT := `████████╗
╚══██╔══╝
   ██║   
   ██║   
   ██║   
   ╚═╝   `

	letterG := ` ██████╗ 
██╔════╝ 
██║  ███╗
██║   ██║
╚██████╔╝
 ╚═════╝ `

	letterC := ` ██████╗
 ██╔════╝
 ██║     
 ██║     
 ╚██████╗
  ╚═════╝`

	letterP := `██████╗
██╔══██╗
██████╔╝
██╔═══╝ 
██║     
╚═╝     `

	// Join them horizontally
	// We need to split them into lines first because simple string concatenation
	// won't work for horizontal joining of multiline strings without Lipgloss's help
	// or manual line-by-line zipping.
	// Thankfully, Lipgloss JoinHorizontal handles rendered blocks nicely.

	blockT := styleT.Render(letterT)
	blockG := styleG.Render(letterG)
	blockC := styleC.Render(letterC)
	blockP := styleP.Render(letterP)

	return lipgloss.JoinHorizontal(lipgloss.Bottom, blockT, blockG, blockC, blockP)
}

// BannerHeight is the number of lines GetBanner() renders — used to decide
// when a terminal is too short for the full ASCII art banner.
const BannerHeight = 6

// GetCompactBanner returns a single-line colored "tgcp" wordmark, used on
// short terminals where the full 6-line ASCII banner won't fit alongside
// the rest of the landing page.
func GetCompactBanner() string {
	return lipgloss.JoinHorizontal(lipgloss.Bottom,
		styleT.Bold(true).Render("t"),
		styleG.Bold(true).Render("g"),
		styleC.Bold(true).Render("c"),
		styleP.Bold(true).Render("p"),
	)
}
