package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// IAMBindingRow is a single role -> members row for the shared IAM bindings
// list view. Every service that adds IAM support renders its own
// []<pkg>.IAMBinding into a []IAMBindingRow to reuse this rendering.
type IAMBindingRow struct {
	Role    string
	Members string // pre-joined, comma-separated
}

// RenderIAMBindings renders a read-only list of current IAM policy bindings
// for a resource, used as the safety-net "look before you grant" view that
// precedes the add-binding form.
func RenderIAMBindings(breadcrumb string, resourceName string, rows []IAMBindingRow) string {
	var b strings.Builder
	b.WriteString(breadcrumb)
	b.WriteString("\n\n")

	if len(rows) == 0 {
		b.WriteString(EmptyState("IAM bindings"))
	} else {
		kvRows := make([]KeyValue, len(rows))
		for i, r := range rows {
			kvRows[i] = KeyValue{Key: r.Role, Value: r.Members}
		}
		b.WriteString(DetailCard(DetailCardOpts{
			Title: fmt.Sprintf("IAM Policy: %s", resourceName),
			Rows:  kvRows,
		}))
	}

	b.WriteString("\n\n")
	b.WriteString(RenderFooterHint("a Add Binding | q Back"))
	return b.String()
}

// NewIAMAddBindingForm builds the shared "add-iam-policy-binding" form: a
// role and a member. Kept deliberately to just these two fields, matching
// `gcloud ... add-iam-policy-binding --role=... --member=...` — the safe
// merge-semantics alternative to a raw set-iam-policy form, since the
// caller is expected to fetch the current policy, append this one binding,
// and set the merged result rather than replacing the whole policy.
func NewIAMAddBindingForm(resourceName string) FormModel {
	return NewForm("Add IAM Binding: "+resourceName, []FormField{
		{Label: "Role", Placeholder: "roles/viewer", Required: true, Validate: validateIAMRole},
		{Label: "Member", Placeholder: "user:name@example.com", Required: true, Validate: validateIAMMember},
	})
}

// validateIAMRole catches the most common typo before it reaches the API: a
// missing `roles/` (predefined) or `.../roles/` (custom role) prefix. It
// deliberately doesn't check the role name itself exists, since that would
// require an API call this form has no way to make.
func validateIAMRole(value string) string {
	if strings.HasPrefix(value, "roles/") ||
		strings.Contains(value, "/roles/") {
		return ""
	}
	return "must start with roles/ (or a custom role path ending in /roles/<name>)"
}

// validateIAMMember catches a missing member-type prefix, the most common
// mistake when hand-typing `gcloud ... --member=` values (e.g. pasting a
// bare email instead of `user:email` or `serviceAccount:email`).
func validateIAMMember(value string) string {
	switch {
	case value == "allUsers", value == "allAuthenticatedUsers":
		return ""
	case strings.HasPrefix(value, "user:"),
		strings.HasPrefix(value, "serviceAccount:"),
		strings.HasPrefix(value, "group:"),
		strings.HasPrefix(value, "domain:"),
		strings.HasPrefix(value, "principal:"),
		strings.HasPrefix(value, "principalSet:"):
		return ""
	default:
		return "must start with user:, serviceAccount:, group:, or domain: (or be allUsers/allAuthenticatedUsers)"
	}
}

// IAMConfirmMessage builds the standard confirmation text for an
// add-iam-policy-binding action, always spelling out the resource, role,
// and member being granted before the user confirms — per this app's
// safety convention for IAM writes.
func IAMConfirmMessage(resourceType, resourceName, role, member string) string {
	return fmt.Sprintf(
		"Grant role %s to %s on %s %s?\n\nThis adds a new binding to the existing policy; it does not replace or remove any current bindings.",
		lipgloss.NewStyle().Bold(true).Render(role),
		lipgloss.NewStyle().Bold(true).Render(member),
		resourceType,
		lipgloss.NewStyle().Bold(true).Render(resourceName),
	)
}
