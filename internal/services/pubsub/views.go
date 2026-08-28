package pubsub

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
	"github.com/yogirk/tgcp/internal/ui/components"
)

// renderConfirmation renders the topic/subscription-delete or IAM-grant
// confirmation dialog.
func (s *Service) renderConfirmation() string {
	if s.pendingAction == "publish" {
		if s.selectedTopic == nil {
			return "Error: No topic selected"
		}
		return components.RenderConfirmationWithMessage(
			"publish",
			s.selectedTopic.Name,
			"topic",
			fmt.Sprintf("Publish this message to topic %s?\n\n%q", s.selectedTopic.Name, s.pendingPublishText),
		)
	}
	if s.pendingAction == "grant" {
		if s.selectedTopic == nil {
			return "Error: No topic selected"
		}
		return components.RenderConfirmationWithMessage(
			"grant",
			s.selectedTopic.Name,
			"topic",
			components.IAMConfirmMessage("topic", s.selectedTopic.Name, s.pendingIAMRole, s.pendingIAMMember),
		)
	}
	if s.actionSource == ViewDetailTopic {
		if s.selectedTopic == nil {
			return "Error: No topic selected"
		}
		return components.RenderConfirmationWithMessage(
			s.pendingAction,
			s.selectedTopic.Name,
			"topic",
			fmt.Sprintf("Are you sure you want to DELETE topic %s? Existing subscriptions are left in place but become detached.", s.selectedTopic.Name),
		)
	}
	if s.selectedSub == nil {
		return "Error: No subscription selected"
	}
	return components.RenderConfirmation(s.pendingAction, s.selectedSub.Name, "subscription")
}

// renderIAMView renders the current IAM policy bindings for the selected
// topic, the safety-net read step before allowing an add-binding write.
func (s *Service) renderIAMView() string {
	if s.selectedTopic == nil {
		return "Error: No topic selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Topics",
		s.selectedTopic.Name,
		"IAM",
	)
	rows := make([]components.IAMBindingRow, len(s.iamBindings))
	for i, b := range s.iamBindings {
		rows[i] = components.IAMBindingRow{Role: b.Role, Members: strings.Join(b.Members, ", ")}
	}
	return components.RenderIAMBindings(breadcrumb, s.selectedTopic.Name, rows)
}

// renderPulledMessages renders the messages returned by the most recent
// no-ack Pull call against the selected subscription. These messages are
// never acknowledged, so pulling again (or a normal subscriber) may see the
// same messages again.
func (s *Service) renderPulledMessages() string {
	if s.selectedSub == nil {
		return "Error: No subscription selected"
	}
	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Subscriptions",
		s.selectedSub.Name,
		"Pulled Messages",
	)

	var body string
	if len(s.pulledMessages) == 0 {
		body = components.EmptyState("messages")
	} else {
		var b strings.Builder
		for i, m := range s.pulledMessages {
			rows := []components.KeyValue{
				{Key: "Message ID", Value: m.MessageID},
				{Key: "Publish Time", Value: m.PublishTime},
				{Key: "Attributes", Value: formatLabels(m.Attributes)},
				{Key: "Data", Value: m.Data},
			}
			card := components.DetailCard(components.DetailCardOpts{
				Title: fmt.Sprintf("Message %d/%d (not acknowledged)", i+1, len(s.pulledMessages)),
				Rows:  rows,
				Width: 70,
			})
			b.WriteString(card)
			b.WriteString("\n")
		}
		body = b.String()
	}

	note := lipgloss.NewStyle().Foreground(styles.ColorTextMuted).Italic(true).
		Render("Messages above were pulled but NOT acknowledged; they remain available for redelivery.")
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", body, note)
}

func (s *Service) View() string {
	if s.err != nil {
		return components.RenderError(s.err, "Pub/Sub", "Topics")
	}

	// Show spinner while loading
	if s.spinner.IsActive() {
		return s.spinner.View()
	}

	if s.viewState == ViewDetailTopic {
		return s.renderDetailTopic()
	}
	if s.viewState == ViewDetailSub {
		return s.renderDetailSub()
	}
	if s.viewState == ViewCreate {
		return s.createForm.View()
	}
	if s.viewState == ViewUpdate {
		return s.updateForm.View()
	}
	if s.viewState == ViewConfirmation {
		return s.renderConfirmation()
	}
	if s.viewState == ViewIAM {
		return s.renderIAMView()
	}
	if s.viewState == ViewIAMForm {
		return s.iamForm.View()
	}
	if s.viewState == ViewPublishForm {
		return s.publishForm.View()
	}
	if s.viewState == ViewPulledMessages {
		return s.renderPulledMessages()
	}

	// Filter Bar
	var content strings.Builder
	listLabel := "Topics"
	if s.viewState == ViewListSubs {
		listLabel = "Subscriptions"
	}
	content.WriteString(components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		listLabel,
	))
	content.WriteString("\n")
	content.WriteString(s.filter.View())
	content.WriteString("\n")
	if s.viewState == ViewListSubs {
		if len(s.subs) == 0 {
			content.WriteString(components.EmptyState("subscriptions"))
			return content.String()
		}
	} else if len(s.topics) == 0 {
		content.WriteString(components.EmptyState("topics"))
		return content.String()
	}
	content.WriteString(s.table.View())
	return content.String()
}

func (s *Service) renderDetailTopic() string {
	t := s.selectedTopic
	if t == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Topics",
		t.Name,
	)

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Topic Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: t.Name},
			{Key: "Project", Value: t.ProjectID},
			{Key: "KMS Key", Value: t.KmsKeyName},
			{Key: "Labels", Value: formatLabels(t.Labels)},
		},
		Width: 60,
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

func (s *Service) renderDetailSub() string {
	sub := s.selectedSub
	if sub == nil {
		return ""
	}

	breadcrumb := components.Breadcrumb(
		fmt.Sprintf("Project %s", s.projectID),
		s.Name(),
		"Subscriptions",
		sub.Name,
	)

	dlqMsg := "None"
	if sub.DeadLetterTopic != "" {
		dlqMsg = styles.ErrorStyle.Render(sub.DeadLetterTopic)
	}

	subType := "Pull"
	if sub.PushEndpoint != "" {
		subType = "Push (" + sub.PushEndpoint + ")"
	}

	card := components.DetailCard(components.DetailCardOpts{
		Title: "Subscription Details",
		Rows: []components.KeyValue{
			{Key: "Name", Value: sub.Name},
			{Key: "Topic", Value: sub.Topic},
			{Key: "State", Value: components.RenderStatus(sub.State)},
			{Key: "Type", Value: subType},
			{Key: "Ack Deadline", Value: fmt.Sprintf("%d sec", sub.AckDeadline)},
			{Key: "Retain Acked", Value: fmt.Sprintf("%v", sub.RetainAcked)},
			{Key: "Message Retention", Value: sub.RetentionDuration},
			{Key: "Dead Letter Topic", Value: dlqMsg},
		},
	})
	return lipgloss.JoinVertical(lipgloss.Left, breadcrumb, "", card)
}

// formatLabels renders a label map as a compact, single-line key=value list
// for display in a detail card row.
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
