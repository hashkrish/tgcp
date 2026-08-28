package components

// globalWidth/globalHeight track the current terminal size so overlay
// dialogs (ConfirmationModel, FormModel) can clamp themselves to it instead
// of assuming an 80x24 terminal. Package-level rather than threaded through
// every service's model because Bubble Tea is single-threaded and dozens of
// services would otherwise each need to track and pass their own copy of the
// size just to render a confirm dialog.
var (
	globalWidth  = 80
	globalHeight = 24
)

// SetGlobalSize records the current terminal size. Call this once, from the
// top-level model's tea.WindowSizeMsg handler.
func SetGlobalSize(width, height int) {
	if width > 0 {
		globalWidth = width
	}
	if height > 0 {
		globalHeight = height
	}
}

// clampDialogWidth returns a box width that fits within the current
// terminal, leaving room for the border/padding margin, without exceeding
// the preferred width.
func clampDialogWidth(preferred, margin int) int {
	if globalWidth-margin < preferred {
		w := globalWidth - margin
		if w < 20 {
			w = 20
		}
		return w
	}
	return preferred
}
