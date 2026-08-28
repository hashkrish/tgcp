package components

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/yogirk/tgcp/internal/styles"
)

// FormField describes a single input field in a FormModel.
type FormField struct {
	Label       string
	Placeholder string
	Default     string
	Required    bool
	// Validate is optional. It runs on submit; a non-empty string is shown
	// as a per-field error and blocks submission.
	Validate func(value string) string
}

// FormModel is a reusable sequential multi-field input form: Tab/Shift+Tab (or
// Up/Down) move between fields, Enter on the last field (or Ctrl+S anywhere)
// submits, Esc cancels. It is the shared pattern for every "Create" flow in
// tgcp -- build one FormModel per resource type, mirror the style used by
// components.ConfirmationModel for surrounding chrome.
type FormModel struct {
	Title  string
	Fields []FormField

	inputs    []textinput.Model
	errs      []string
	focus     int
	Width     int
	Height    int
	SubmitErr string // top-level error shown below the fields (e.g. API error)
}

// NewForm builds a FormModel from field definitions. Fields are focused in
// order starting at index 0.
func NewForm(title string, fields []FormField) FormModel {
	inputs := make([]textinput.Model, len(fields))
	for i, f := range fields {
		ti := textinput.New()
		ti.Placeholder = f.Placeholder
		ti.SetValue(f.Default)
		ti.CharLimit = 256
		ti.Width = 40
		if i == 0 {
			ti.Focus()
		}
		inputs[i] = ti
	}
	return FormModel{
		Title:  title,
		Fields: fields,
		inputs: inputs,
		errs:   make([]string, len(fields)),
		focus:  0,
	}
}

// Values returns the current value of every field, keyed by its Label.
func (m FormModel) Values() map[string]string {
	out := make(map[string]string, len(m.inputs))
	for i, f := range m.Fields {
		out[f.Label] = strings.TrimSpace(m.inputs[i].Value())
	}
	return out
}

// Value returns the current value of the field with the given label, or "".
func (m FormModel) Value(label string) string {
	for i, f := range m.Fields {
		if f.Label == label {
			return strings.TrimSpace(m.inputs[i].Value())
		}
	}
	return ""
}

// Validate runs each field's Required/Validate check and stores per-field
// errors. It returns true if the form is valid and ready to submit.
func (m *FormModel) Validate() bool {
	ok := true
	for i, f := range m.Fields {
		val := strings.TrimSpace(m.inputs[i].Value())
		m.errs[i] = ""
		if f.Required && val == "" {
			m.errs[i] = "required"
			ok = false
			continue
		}
		if f.Validate != nil {
			if errText := f.Validate(val); errText != "" {
				m.errs[i] = errText
				ok = false
			}
		}
	}
	return ok
}

// FormResult reports the outcome of a keypress handed to Update.
type FormResult struct {
	// Submitted is true when Enter/Ctrl+S was pressed on a valid form.
	Submitted bool
	// Cancelled is true when Esc was pressed.
	Cancelled bool
	// Handled is true if the key was consumed by the form and should not be
	// processed further by the owning service.
	Handled bool
}

// Update processes a key message against the form. The caller (a service's
// Update) should call this while its view state is "showing the create
// form", and act on the returned FormResult: on Submitted, call m.Validate()
// (already done internally) and read m.Values() / m.Value(...) to fire the
// create API call; on Cancelled, return to the list/detail view.
func (m *FormModel) Update(msg tea.Msg) (FormResult, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		if m.focus >= 0 && m.focus < len(m.inputs) {
			m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
		}
		return FormResult{}, cmd
	}

	switch keyMsg.String() {
	case "esc":
		return FormResult{Cancelled: true, Handled: true}, nil
	case "ctrl+s":
		if m.Validate() {
			return FormResult{Submitted: true, Handled: true}, nil
		}
		return FormResult{Handled: true}, nil
	case "tab", "down":
		m.blurCurrent()
		m.focus = (m.focus + 1) % len(m.inputs)
		m.focusCurrent()
		return FormResult{Handled: true}, nil
	case "shift+tab", "up":
		m.blurCurrent()
		m.focus--
		if m.focus < 0 {
			m.focus = len(m.inputs) - 1
		}
		m.focusCurrent()
		return FormResult{Handled: true}, nil
	case "enter":
		if m.focus == len(m.inputs)-1 {
			if m.Validate() {
				return FormResult{Submitted: true, Handled: true}, nil
			}
			return FormResult{Handled: true}, nil
		}
		m.blurCurrent()
		m.focus = (m.focus + 1) % len(m.inputs)
		m.focusCurrent()
		return FormResult{Handled: true}, nil
	}

	var cmd tea.Cmd
	if m.focus >= 0 && m.focus < len(m.inputs) {
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(msg)
	}
	return FormResult{Handled: true}, cmd
}

func (m *FormModel) blurCurrent() {
	if m.focus >= 0 && m.focus < len(m.inputs) {
		m.inputs[m.focus].Blur()
	}
}

func (m *FormModel) focusCurrent() {
	if m.focus >= 0 && m.focus < len(m.inputs) {
		m.inputs[m.focus].Focus()
	}
}

// Reset clears every field back to its Default and refocuses the first field.
func (m *FormModel) Reset() {
	for i, f := range m.Fields {
		m.inputs[i].SetValue(f.Default)
		m.inputs[i].Blur()
		m.errs[i] = ""
	}
	m.focus = 0
	m.SubmitErr = ""
	if len(m.inputs) > 0 {
		m.inputs[0].Focus()
	}
}

// SetSize updates the component's rendering size.
func (m *FormModel) SetSize(width, height int) {
	m.Width = width
	m.Height = height
}

// View renders the form as a centered, bordered dialog -- the create-form
// counterpart to components.RenderConfirmation.
func (m FormModel) View() string {
	titleStyle := lipgloss.NewStyle().Foreground(styles.ColorInfo).Bold(true)
	title := titleStyle.Render("+ " + m.Title)

	labelStyle := styles.SubtleStyle
	errStyle := lipgloss.NewStyle().Foreground(styles.ColorError)

	var rows []string
	rows = append(rows, title, "")
	for i, f := range m.Fields {
		label := f.Label
		if f.Required {
			label += " *"
		}
		line := fmt.Sprintf("%s\n%s", labelStyle.Render(label), m.inputs[i].View())
		if m.errs[i] != "" {
			line += "  " + errStyle.Render(m.errs[i])
		}
		rows = append(rows, line)
	}

	if m.SubmitErr != "" {
		rows = append(rows, "", errStyle.Render(m.SubmitErr))
	}

	rows = append(rows, "", RenderFooterHint("Tab/↑↓ Move | Enter/Ctrl+S Submit | Esc Cancel"))

	content := lipgloss.JoinVertical(lipgloss.Left, rows...)

	dialog := styles.OverlayBoxStyle.
		BorderForeground(styles.ColorInfo).
		Padding(styles.SpaceS, styles.SpaceL).
		Width(clampDialogWidth(60, 4)).
		Render(content)

	return lipgloss.Place(
		globalWidth, globalHeight,
		lipgloss.Center, lipgloss.Center,
		dialog,
	)
}
