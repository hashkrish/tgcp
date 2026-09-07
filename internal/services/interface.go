package services

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// Service represents a pluggable GCP service module
type Service interface {
	// Name returns the display name (e.g. "Google Compute Engine")
	Name() string

	// ShortName returns the ID for command palette (e.g. "gce")
	ShortName() string

	// InitService initializes the service (API clients, empty state)
	InitService(ctx context.Context, projectID string) error

	// Reinit reinitializes the service with a new project ID
	// This is called when switching projects and should reset state and reinitialize clients
	Reinit(ctx context.Context, projectID string) error

	// Update handles messages specific to this service
	Update(msg tea.Msg) (tea.Model, tea.Cmd)

	// View renders the service UI
	View() string

	// HelpText returns the context-aware help text for the status bar
	HelpText() string

	// Refresh triggers a data reload
	Refresh() tea.Cmd

	// Focus is called when the service gains input focus
	Focus()

	// Blur is called when the service loses input focus
	Blur()

	// Reset resets the service state (e.g. back to list view)
	Reset()

	// IsRootView returns true if the service is at its top-level view (e.g. List)
	// Used to determine if 'q' should exit the service or go back
	IsRootView() bool
}

// TabCycler is implemented by services with their own internal sub-tabs
// (e.g. Cloud Run's Services/Functions tabs, Cloud Logging's resource
// tabs). The top-level model calls NextTab/PrevTab directly for a real
// Tab/Shift+Tab keypress instead of re-injecting a synthetic "]"/"["
// keystroke through Update -- each service decides for itself, from its
// own state, whether Tab means "cycle tabs" right now, so a Tab that
// actually belongs to a focused text input (a filter box, a form field)
// can never be misinterpreted as a tab switch (or vice versa).
type TabCycler interface {
	// NextTab/PrevTab cycle to the adjacent tab if that's currently
	// appropriate (e.g. the plain list view, not a filter or a form). ok
	// reports whether it did; when false, the real Tab/Shift+Tab keystroke
	// falls through to the service's normal Update handling instead (e.g.
	// a form's own "next field" behavior).
	NextTab() (tea.Cmd, bool)
	PrevTab() (tea.Cmd, bool)
}
