package gce

import (
	"fmt"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/yogirk/tgcp/internal/utils"
)

// Action messages
type actionResultMsg struct {
	err error
	msg string

	// resource/name/action describe the mutating call this result came
	// from, so the actionResultMsg handler in gce.go can record a
	// core.Job -- captured here rather than read back off s.pendingAction
	// because that field is already reset to "" by the time the result
	// comes back (see the ViewConfirmation "y"/"enter" handling).
	resource string
	name     string
	action   string
}

// StartInstanceCmd triggers the start operation
func (s *Service) StartInstanceCmd(instance Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "instance", name: instance.Name, action: "start"}
		}
		err := s.client.StartInstance(s.projectID, instance.Zone, instance.Name)
		if err != nil {
			return actionResultMsg{err: err, resource: "instance", name: instance.Name, action: "start"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Starting instance %s...", instance.Name), resource: "instance", name: instance.Name, action: "start"}
	}
}

// StopInstanceCmd triggers the stop operation
func (s *Service) StopInstanceCmd(instance Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "instance", name: instance.Name, action: "stop"}
		}
		err := s.client.StopInstance(s.projectID, instance.Zone, instance.Name)
		if err != nil {
			return actionResultMsg{err: err, resource: "instance", name: instance.Name, action: "stop"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Stopping instance %s...", instance.Name), resource: "instance", name: instance.Name, action: "stop"}
	}
}

// ResetInstanceCmd triggers a hard reset of the given instance
func (s *Service) ResetInstanceCmd(instance Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "instance", name: instance.Name, action: "reset"}
		}
		if err := s.client.ResetInstance(s.projectID, instance.Zone, instance.Name); err != nil {
			return actionResultMsg{err: err, resource: "instance", name: instance.Name, action: "reset"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resetting instance %s...", instance.Name), resource: "instance", name: instance.Name, action: "reset"}
	}
}

// SuspendInstanceCmd triggers suspending the given instance to disk
func (s *Service) SuspendInstanceCmd(instance Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "instance", name: instance.Name, action: "suspend"}
		}
		if err := s.client.SuspendInstance(s.projectID, instance.Zone, instance.Name); err != nil {
			return actionResultMsg{err: err, resource: "instance", name: instance.Name, action: "suspend"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Suspending instance %s...", instance.Name), resource: "instance", name: instance.Name, action: "suspend"}
	}
}

// ResumeInstanceCmd triggers resuming the given suspended instance
func (s *Service) ResumeInstanceCmd(instance Instance) tea.Cmd {
	return func() tea.Msg {
		if s.client == nil {
			return actionResultMsg{err: fmt.Errorf("client not initialized"), resource: "instance", name: instance.Name, action: "resume"}
		}
		if err := s.client.ResumeInstance(s.projectID, instance.Zone, instance.Name); err != nil {
			return actionResultMsg{err: err, resource: "instance", name: instance.Name, action: "resume"}
		}
		return actionResultMsg{msg: fmt.Sprintf("Resuming instance %s...", instance.Name), resource: "instance", name: instance.Name, action: "resume"}
	}
}

// SSHCmd constructs the gcloud ssh command
func (s *Service) SSHCmd(instance Instance) tea.Cmd {
	// Build base arguments
	args := []string{"compute", "ssh", instance.Name, "--zone", instance.Zone, "--project", s.projectID}

	// Auto-detect IAP: If no external IP, use IAP tunnel
	if instance.ExternalIP == "" {
		args = append(args, "--tunnel-through-iap")
	}

	// Check for Tmux
	if utils.IsTmux() {
		return func() tea.Msg {
			// Use -- to prevent shell interpretation of arguments
			tmuxArgs := append([]string{"split-window", "-h", "--", "gcloud"}, args...)
			cmd := exec.Command("tmux", tmuxArgs...)

			if err := cmd.Run(); err != nil {
				return actionResultMsg{err: fmt.Errorf("tmux split failed: %w", err)}
			}
			return actionResultMsg{msg: "Opened SSH in new pane"}
		}
	}

	// Standard Full Screen SSH
	cmd := exec.Command("gcloud", args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return actionResultMsg{err: fmt.Errorf("SSH failed: %w", err)}
		}
		return actionResultMsg{msg: "SSH session ended"}
	})
}
