package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// Filter messages
type filterModeEnteredMsg struct{}
type filterModeExitedMsg struct{}
type filterTextChangedMsg struct {
	text string
}

// FilterModel represents the filter state
// type FilterModel struct {
// 	active bool
// 	input  textinput.Model
// }

// NewFilterModel creates a new filter model
// func NewFilterModel() FilterModel {
// 	ti := textinput.New()
// 	ti.Placeholder = "Filter..."
// 	ti.CharLimit = 50
// 	ti.Width = 30

// 	return FilterModel{
// 		active: false,
// 		input:  ti,
// 	}
// }

// enterFilterMode enters filter mode
func (m Model) enterFilterMode() (Model, tea.Cmd) {
	if m.activePanel != 1 { // Only allow filtering in center panel
		return m, nil
	}

	// Enable filtering - the list component handles the input
	m.centerPanel.SetFilteringEnabled(true)

	return m, nil
}

// exitFilterMode exits filter mode
func (m Model) exitFilterMode() (Model, tea.Cmd) {
	m.centerPanel.SetFilteringEnabled(false)
	m.centerPanel.ResetFilter()

	return m, nil
}

// handleFilterInput handles filter input in the Update function
func (m Model) handleFilterInput(msg tea.KeyMsg) (Model, tea.Cmd) {
	if !m.centerPanel.FilteringEnabled() {
		return m, nil
	}

	switch msg.String() {
	case "esc":
		return m.exitFilterMode()
	default:
		// Let the list component handle the filtering
		var cmd tea.Cmd
		m.centerPanel, cmd = m.centerPanel.Update(msg)
		return m, cmd
	}
}

// isFilterKey checks if a key should trigger filter mode
func isFilterKey(msg tea.KeyMsg) bool {
	return msg.String() == "/"
}