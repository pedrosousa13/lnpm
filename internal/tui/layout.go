package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// switchToPackages switches to the packages view
func (m Model) switchToPackages() (Model, tea.Cmd) {
	m.currentView = 0
	m.loading = true
	m.centerPanel.Title = "Loading packages..."
	m.centerPanel.SetDelegate(createPackageDelegate())
	return m, loadPackagesCmd()
}

// switchToCurrentLinks switches to the current project links view
func (m Model) switchToCurrentLinks() (Model, tea.Cmd) {
	m.currentView = 1 // Now at index 1
	m.loading = true
	m.centerPanel.Title = "Loading current links..."
	m.centerPanel.SetDelegate(createLinkDelegate())
	return m, loadCurrentLinksCmd()
}

// handleNavigationSelection handles selection in the navigation panel
// Deprecated: Navigation panel is removed, but keeping method sig if referenced elsewhere
func (m Model) handleNavigationSelection() (Model, tea.Cmd) {
	return m, nil
}