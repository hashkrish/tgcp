package appengine

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
	"google.golang.org/api/googleapi"
)

// View renders the service UI.
func (s *Service) View() string {
	if s.err != nil {
		if title, body, ok := friendlyServicesError(s.err, s.projectID); ok {
			return components.RenderNotice(title, body, "r Refresh  |  q Back", styles.ColorWarning)
		}
		return components.RenderError(s.err, s.Name(), "Services")
	}

	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	switch s.viewState {
	case ViewVersions:
		return s.renderVersionsView()
	case ViewVersionDetail:
		return s.renderVersionDetailView()
	case ViewInstances:
		return s.renderInstancesView()
	case ViewApplication:
		return s.renderApplicationView()
	case ViewSplitTraffic:
		return s.splitForm.View()
	case ViewConfirmation:
		return s.renderConfirmation()
	}

	return s.renderServicesView()
}

// friendlyServicesError recognizes the two expected/recoverable App Engine
// API failure classes -- not-found (404) and denied (403) -- and returns a
// calmer explanation for RenderNotice instead of RenderError's generic
// unexpected-failure box. It always includes Google's own error message
// (gerr.Message), which for a 403 reliably distinguishes "SERVICE_DISABLED"
// (the App Engine Admin API -- a distinct API from App Engine itself --
// isn't enabled on this project in Service Usage) from an actual IAM
// permission gap: confirmed by direct reproduction that `gcloud`/plain
// bearer-token REST calls can silently succeed against a *different*
// (already-enabled) default quota-project context while this tool's client
// library correctly attaches an X-Goog-User-Project header for the real
// target project and gets the true, honest answer -- so "it works in
// gcloud/Console" does not rule this out, and is in fact the expected
// symptom of exactly this cause. ok is false for any other error, which
// callers should fall back to RenderError for.
func friendlyServicesError(err error, projectID string) (title, body string, ok bool) {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return "", "", false
	}
	switch gerr.Code {
	case http.StatusNotFound:
		return "App Engine Not Found", fmt.Sprintf(
			"Google's error: %s\n\n"+
				"This usually means App Engine hasn't been enabled on project %q yet:\n"+
				"  gcloud app create --project=%s\n\n"+
				"But it can also mean tgcp is pointed at the wrong project. Then press r to check again.",
			gerr.Message, projectID, projectID,
		), true
	case http.StatusForbidden:
		if strings.Contains(gerr.Message, "has not been used in project") || strings.Contains(gerr.Message, "SERVICE_DISABLED") {
			return "App Engine Admin API Disabled", fmt.Sprintf(
				"Google's error: %s\n\n"+
					"The App Engine Admin API is a separate API from App Engine itself, and isn't enabled on project %q yet -- even though App Engine already serves traffic and works fine in `gcloud`/Console (those can use a different quota-project context that hides this). Enable it with:\n\n"+
					"  gcloud services enable appengine.googleapis.com --project=%s\n\n"+
					"Then press r to check again.",
				gerr.Message, projectID, projectID,
			), true
		}
		return "Permission Denied", fmt.Sprintf(
			"Google's error: %s\n\n"+
				"Ask your project admin to grant the roles/appengine.appViewer (or roles/appengine.appAdmin) IAM role for %q, then press r to check again.",
			gerr.Message, projectID,
		), true
	}
	return "", "", false
}

// renderServicesView renders the root Services list.
func (s *Service) renderServicesView() string {
	breadcrumb := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Services")
	filterBar := s.filter.View() + "\n"

	if len(s.services) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, filterBar, components.EmptyState("services"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, filterBar, s.table.View())
}

// renderVersionsView renders the list of a service's versions.
func (s *Service) renderVersionsView() string {
	if s.selectedService == nil {
		return "No service selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Id,
		"Versions",
	)

	if len(s.currentVersions) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("versions"))
	}

	hint := "s Start  |  x Stop  |  d Delete  |  I Instances  |  l Logs  |  r Refresh  |  enter Details  |  q Back"
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.versionTable.View(), "", styles.HelpStyle.Render(hint))
}

// renderVersionDetailView renders full details for a single version.
func (s *Service) renderVersionDetailView() string {
	if s.selectedService == nil || s.selectedVersion == nil {
		return "No version selected"
	}
	v := s.selectedVersion

	title := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Id,
		"Versions",
		v.Id,
	)

	created := ""
	if !v.CreateTime.IsZero() {
		created = v.CreateTime.Format("2006-01-02 15:04")
	}
	traffic := "-"
	if v.Traffic > 0 {
		traffic = fmt.Sprintf("%.0f%%", v.Traffic*100)
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Version Details",
		Rows: []components.KeyValue{
			{Key: "Id", Value: v.Id},
			{Key: "Status", Value: components.RenderStatus(v.ServingStatus)},
			{Key: "Traffic", Value: traffic},
			{Key: "Runtime", Value: v.Runtime},
			{Key: "Environment", Value: v.Env},
			{Key: "Instance Class", Value: v.InstanceClass},
			{Key: "Threadsafe", Value: fmt.Sprintf("%t", v.Threadsafe)},
			{Key: "Created", Value: created},
			{Key: "URL", Value: v.VersionUrl},
		},
		FooterHint: "s Start  |  x Stop  |  d Delete  |  I Instances  |  l Logs  |  q Back",
	})

	return lipgloss.JoinVertical(lipgloss.Left, title, "", card)
}

// renderInstancesView renders the list of a version's running instances.
func (s *Service) renderInstancesView() string {
	if s.selectedService == nil || s.selectedVersion == nil {
		return "No version selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Services",
		s.selectedService.Id,
		"Versions",
		s.selectedVersion.Id,
		"Instances",
	)

	if len(s.instances) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", components.EmptyState("instances"))
	}

	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", s.instanceTable.View())
}

// renderApplicationView renders the project's single App Engine Application.
func (s *Service) renderApplicationView() string {
	if s.application == nil {
		return "No application info"
	}
	app := s.application

	title := components.Breadcrumb(fmt.Sprintf("Project %s", s.projectID), s.Name(), "Application")

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Application Details",
		Rows: []components.KeyValue{
			{Key: "Id", Value: app.Id},
			{Key: "Default Hostname", Value: app.DefaultHostname},
			{Key: "Location", Value: app.LocationId},
			{Key: "Serving Status", Value: components.RenderStatus(app.ServingStatus)},
		},
		FooterHint: "q Back",
	})

	return lipgloss.JoinVertical(lipgloss.Left, title, "", card)
}

// renderConfirmation renders the confirmation dialog for the pending action.
func (s *Service) renderConfirmation() string {
	switch s.pendingAction {
	case "delete-service":
		if s.selectedService == nil {
			return "Error: no service selected"
		}
		message := fmt.Sprintf("Delete service %s and all its versions? This cannot be undone.", styles.TitleStyle.Render(s.selectedService.Id))
		if s.selectedService.Id == "default" {
			message += "\n\nWarning: this is the default service."
		}
		return components.RenderConfirmationWithMessage("delete", s.selectedService.Id, "service", message)
	case "start-version":
		if s.selectedVersion == nil {
			return "Error: no version selected"
		}
		return components.RenderConfirmation("start", s.selectedVersion.Id, "version")
	case "stop-version":
		if s.selectedVersion == nil {
			return "Error: no version selected"
		}
		return components.RenderConfirmation("stop", s.selectedVersion.Id, "version")
	case "delete-version":
		if s.selectedVersion == nil {
			return "Error: no version selected"
		}
		return components.RenderConfirmation("delete", s.selectedVersion.Id, "version")
	case "split-traffic":
		if s.selectedService == nil {
			return "Error: no service selected"
		}
		return components.RenderConfirmation("split-traffic", s.selectedService.Id, "service")
	}
	return "Unknown pending action"
}
