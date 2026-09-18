package components

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/yogirk/tgcp/internal/core"
)

// CopyToClipboardCmd copies value to the system clipboard using an OSC 52
// terminal escape sequence. Unlike a local clipboard utility (pbcopy/xclip),
// OSC 52 is honored by the terminal emulator itself, so it also works over
// SSH and inside tmux. Not every terminal supports it, so the returned toast
// is optimistic feedback rather than a confirmed result.
func CopyToClipboardCmd(label, value string) tea.Cmd {
	label = strings.TrimSpace(label)
	return func() tea.Msg {
		if value == "" {
			return core.ToastMsg{Message: fmt.Sprintf("%s is empty, nothing to copy", label), Type: core.ToastInfo}
		}
		termenv.Copy(value)
		return core.ToastMsg{Message: fmt.Sprintf("Copied %s to clipboard", label), Type: core.ToastSuccess}
	}
}
