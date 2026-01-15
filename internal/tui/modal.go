package tui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ModalType defines the type of modal dialog
type ModalType int

const (
	ModalNone       ModalType = iota
	ModalConfirm              // Yes/No confirmation
	ModalHelp                 // Help screen
	ModalSuccess              // Success message
	ModalError                // Error message
)

// Modal represents a modal dialog
type Modal struct {
	Type       ModalType
	Title      string
	Message    string
	Details    string // Additional details or help text
	Options    []string // Button labels (typically "Yes"/"No" or "OK")
	Selected   int      // Currently selected option
	Width      int
	Height     int
}

// NewConfirmModal creates a confirmation modal
func NewConfirmModal(title, message string, options []string) *Modal {
	if len(options) == 0 {
		options = []string{"Yes", "No"}
	}
	return &Modal{
		Type:     ModalConfirm,
		Title:    title,
		Message:  message,
		Options:  options,
		Selected: 1, // Default to "No" for safety
	}
}

// NewHelpModal creates a help screen modal
func NewHelpModal() *Modal {
	return &Modal{
		Type:   ModalHelp,
		Title:  "Help - Keybindings",
		Message: buildHelpText(),
	}
}

// NewSuccessModal creates a success message modal
func NewSuccessModal(message string) *Modal {
	return &Modal{
		Type:    ModalSuccess,
		Title:   "Success",
		Message: message,
		Options: []string{"OK"},
	}
}

// NewErrorModal creates an error message modal
func NewErrorModal(title, message string) *Modal {
	return &Modal{
		Type:    ModalError,
		Title:   title,
		Message: message,
		Options: []string{"OK"},
	}
}

// View renders the modal
func (m *Modal) View() string {
	if m == nil || m.Type == ModalNone {
		return ""
	}

	switch m.Type {
	case ModalConfirm:
		return m.renderConfirmModal()
	case ModalHelp:
		return m.renderHelpModal()
	case ModalSuccess:
		return m.renderSuccessModal()
	case ModalError:
		return m.renderErrorModal()
	default:
		return ""
	}
}

// renderConfirmModal renders a confirmation dialog
func (m *Modal) renderConfirmModal() string {
	var content string

	// Title
	content += lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Render("⚠️  " + m.Title) + "\n\n"

	// Message
	content += m.Message + "\n\n"

	// Options (buttons)
	var buttons []string
	for i, opt := range m.Options {
		style := lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorPrimary)

		if i == m.Selected {
			style = style.
				Background(colorActive).
				Foreground(colorInverted)
		} else {
			style = style.
				Background(lipgloss.Color("240")).
				Foreground(colorPrimary)
		}

		buttons = append(buttons, style.Render(opt))
	}

	content += lipgloss.JoinHorizontal(lipgloss.Center, buttons...)

	// Wrap in box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(2, 4).
		Width(m.Width / 2)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box.Render(content))
}

// renderHelpModal renders the help screen
func (m *Modal) renderHelpModal() string {
	var content string

	// Title
	content += lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 0, 1, 0).
		Render("⌨️  Keybindings & Help") + "\n"

	content += m.Message

	// Footer
	content += "\n" + lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render("Press 'q' or ESC to close") + "\n"

	// Wrap in box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorPrimary).
		Padding(2, 4).
		Width(m.Width - 4)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Top, lipgloss.Left, box.Render(content))
}

// renderSuccessModal renders a success message
func (m *Modal) renderSuccessModal() string {
	var content string

	// Title
	content += lipgloss.NewStyle().
		Foreground(colorSuccess).
		Bold(true).
		Render("✅ " + m.Title) + "\n\n"

	// Message
	content += m.Message + "\n\n"

	// OK button
	button := lipgloss.NewStyle().
		Padding(0, 2).
		Background(colorSuccess).
		Foreground(colorBg).
		Render("OK")

	content += button

	// Wrap in box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colorSuccess).
		Padding(2, 4).
		Width(m.Width / 2)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box.Render(content))
}

// renderErrorModal renders an error message
func (m *Modal) renderErrorModal() string {
	var content string

	// Title
	content += lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Bold(true).
		Render("❌ " + m.Title) + "\n\n"

	// Message
	content += m.Message + "\n\n"

	// OK button
	button := lipgloss.NewStyle().
		Padding(0, 2).
		Background(lipgloss.Color("196")).
		Foreground(colorBg).
		Render("OK")

	content += button

	// Wrap in box
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("196")).
		Padding(2, 4).
		Width(m.Width / 2)

	return lipgloss.Place(m.Width, m.Height, lipgloss.Center, lipgloss.Center, box.Render(content))
}

// Update handles user input for the modal
func (m *Modal) Update(msg tea.Msg) (bool, tea.Cmd) {
	// Returns: (shouldClose, cmd)

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc":
			return true, nil

		case "h", "left":
			if m.Selected > 0 {
				m.Selected--
			}

		case "l", "right":
			if m.Selected < len(m.Options)-1 {
				m.Selected++
			}

		case "enter":
			return true, nil
		}
	}

	return false, nil
}

// GetSelectedOption returns the index of the selected option
func (m *Modal) GetSelectedOption() int {
	return m.Selected
}

// buildHelpText creates the help screen text
func buildHelpText() string {
	var help string

	help += "┌─────────────────────────────────────────────────────────────┐\n"
	help += "│                    NAVIGATION & PANELS                      │\n"
	help += "└─────────────────────────────────────────────────────────────┘\n"
	help += "  LEFT PANEL (Navigation)     - Choose which view to display\n"
	help += "  CENTER PANEL (Main List)    - Browse packages/projects/links\n"
	help += "  RIGHT PANEL (Details)       - See details of selected item\n\n"

	help += "  j/k or ↑↓          Navigate up/down in current list\n"
	help += "  h/l or ←→          Move focus between panels\n"
	help += "  Tab                Cycle to next panel\n"
	help += "  Enter              Select item in navigation\n"
	help += "  g/G                Jump to top/bottom of list\n"
	help += "  /                  Search/filter current list\n\n"

	help += "┌─────────────────────────────────────────────────────────────┐\n"
	help += "│                     ACTIONS BY VIEW                         │\n"
	help += "└─────────────────────────────────────────────────────────────┘\n"
	help += "  PACKAGES VIEW:\n"
	help += "    p               Push package to all linked projects\n"
	help += "    o               Open package source folder\n"
	help += "    u               Update package version\n"
	help += "    r               Remove all package links\n\n"

	help += "  PROJECTS VIEW:\n"
	help += "    Space           Toggle selection for batch operations\n"
	help += "    r               Remove selected links\n"
	help += "    p               Push selected package to projects\n\n"

	help += "  CURRENT LINKS VIEW:\n"
	help += "    r               Remove the selected link\n"
	help += "    p               Push link update to project\n"
	help += "    Space           Toggle selection for batch removal\n\n"

	help += "┌─────────────────────────────────────────────────────────────┐\n"
	help += "│                      GLOBAL CONTROLS                        │\n"
	help += "└─────────────────────────────────────────────────────────────┘\n"
	help += "  ?                  Show this help screen\n"
	help += "  Ctrl+R             Refresh data from database\n"
	help += "  y/n                Confirm/cancel dialogs (when shown)\n"
	help += "  q or Ctrl+C        Quit the application\n\n"

	help += "┌─────────────────────────────────────────────────────────────┐\n"
	help += "│                         TIPS                                │\n"
	help += "└─────────────────────────────────────────────────────────────┘\n"
	help += "  • Right panel shows detailed info about selected items\n"
	help += "  • Actions ask for confirmation before executing\n"
	help += "  • Batch operations are efficient (single npm install)\n"
	help += "  • Use Ctrl+R to update data from database\n"

	return help
}
