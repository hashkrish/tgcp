package core

import (
	"strings"

	"github.com/sahilm/fuzzy"
)

// ViewType enumerates available views
type ViewType int

const (
	ViewHome ViewType = iota
	ViewServiceList
	ViewResourceDetail
	ViewHelp
	ViewProjectSwitcher
)

// Route represents a navigational destination
type Route struct {
	View    ViewType
	Service string // e.g., "gce", "sql"
	ID      string // resource ID or Project ID
}

// Command represents an actionable command in the palette
type Command struct {
	Name        string
	Description string
	Action      func() Route
}

// SuggestionMatch wraps a Command with fuzzy match info for highlighting
type SuggestionMatch struct {
	Command
	MatchedIndexes []int // Positions of matched characters in Name+Description
}

// NavigationModel manages routing and command palette state
type NavigationModel struct {
	CurrentRoute Route
	History      []Route
	Commands     []Command
	BaseCommands []Command // Persist default commands

	// Palette State
	PaletteActive bool
	Query         string
	Suggestions   []SuggestionMatch // Includes match info for highlighting
	Selection     int
}

func NewNavigation() NavigationModel {
	defaults := defaultCommands()
	return NavigationModel{
		CurrentRoute:  Route{View: ViewHome},
		History:       make([]Route, 0),
		Commands:      defaults,
		BaseCommands:  defaults,
		PaletteActive: false,
		Suggestions:   []SuggestionMatch{},
	}
}

// defaultCommands returns the small set of non-service commands that always
// exist, used as a placeholder until SetBaseCommands installs the real,
// dynamically-built list (see InitialModel in internal/ui/model.go) — the
// per-service entries used to be hardcoded here too, which silently drifted
// out of sync as new services were registered over time (several were
// missing from the palette entirely). Services are now derived from the
// actual service registry instead, so this can never happen again.
func defaultCommands() []Command {
	return []Command{
		{Name: "GCP: Switch Project", Description: "Switch active Google Cloud Project", Action: func() Route { return Route{View: ViewProjectSwitcher} }},
		{Name: "Home", Description: "Go to Home Screen", Action: func() Route { return Route{View: ViewHome} }},
		{Name: "Help", Description: "Show Help Screen", Action: func() Route { return Route{View: ViewHelp} }},
	}
}

// SetCommands updates the available commands (e.g. for switching context)
func (m *NavigationModel) SetCommands(cmds []Command) {
	m.Commands = cmds
	m.Selection = 0
	m.FilterCommands("") // Reset filter
}

// SetBaseCommands replaces the permanent default command set — used once at
// startup, after the actual registered services are known, so the palette
// can never drift out of sync with what's really available (unlike the old
// hardcoded defaultCommands() list, which silently stopped covering new
// services as they were added over time). Also applies the new set as the
// active one immediately.
func (m *NavigationModel) SetBaseCommands(cmds []Command) {
	m.BaseCommands = cmds
	m.SetCommands(cmds)
}

// RestoreBaseCommands resets to default commands
func (m *NavigationModel) RestoreBaseCommands() {
	m.Commands = m.BaseCommands
	m.Selection = 0
	m.FilterCommands("")
}

// FilterCommands updates suggestions based on input query.
//
// Ranking is a three-tier cascade so that literal matches always beat
// loose fuzzy hits — critical for short queries like "gce" where a plain
// fuzzy rank would float "GKE" above "GCE":
//
//  1. Prefix match on Name (case-insensitive)
//  2. Substring match anywhere in Name
//  3. Fuzzy match on "Name + Description" for everything else
//
// Within each tier, results preserve registration order from defaultCommands.
func (m *NavigationModel) FilterCommands(query string) {
	m.Query = query
	if query == "" {
		m.Suggestions = []SuggestionMatch{}
		return
	}

	queryLower := strings.ToLower(query)
	seen := make(map[int]bool, len(m.Commands))
	var suggestions []SuggestionMatch

	// Tier 1: prefix match on Name
	for i, cmd := range m.Commands {
		nameLower := strings.ToLower(cmd.Name)
		if strings.HasPrefix(nameLower, queryLower) {
			suggestions = append(suggestions, SuggestionMatch{
				Command:        cmd,
				MatchedIndexes: rangeIndexes(0, len(query)),
			})
			seen[i] = true
		}
	}

	// Tier 2: substring match anywhere in Name
	for i, cmd := range m.Commands {
		if seen[i] {
			continue
		}
		nameLower := strings.ToLower(cmd.Name)
		if idx := strings.Index(nameLower, queryLower); idx >= 0 {
			suggestions = append(suggestions, SuggestionMatch{
				Command:        cmd,
				MatchedIndexes: rangeIndexes(idx, idx+len(query)),
			})
			seen[i] = true
		}
	}

	// Tier 3: fuzzy match on Name + Description for anything left
	var remainingSources []string
	var remainingOrigIdx []int
	for i, cmd := range m.Commands {
		if seen[i] {
			continue
		}
		remainingSources = append(remainingSources, cmd.Name+" "+cmd.Description)
		remainingOrigIdx = append(remainingOrigIdx, i)
	}
	for _, match := range fuzzy.Find(query, remainingSources) {
		origIdx := remainingOrigIdx[match.Index]
		suggestions = append(suggestions, SuggestionMatch{
			Command:        m.Commands[origIdx],
			MatchedIndexes: match.MatchedIndexes,
		})
	}

	m.Suggestions = suggestions
	m.Selection = 0 // Reset selection
}

// rangeIndexes returns a slice of sequential ints from start (inclusive) to
// end (exclusive). Used to build MatchedIndexes for literal prefix/substring
// hits so palette rendering highlights the matched span uniformly.
func rangeIndexes(start, end int) []int {
	out := make([]int, end-start)
	for i := range out {
		out[i] = start + i
	}
	return out
}

// SelectNext moves selection down
func (m *NavigationModel) SelectNext() {
	if m.Selection < len(m.Suggestions)-1 {
		m.Selection++
	}
}

// SelectPrev moves selection up
func (m *NavigationModel) SelectPrev() {
	if m.Selection > 0 {
		m.Selection--
	}
}

// ExecuteSelection returns the route for the selected command
func (m *NavigationModel) ExecuteSelection() *Route {
	if len(m.Suggestions) > 0 {
		route := m.Suggestions[m.Selection].Action()
		return &route
	}
	return nil
}
