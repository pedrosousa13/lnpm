package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// createPackageDelegate creates a custom delegate for package items
func createPackageDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.SetHeight(1) // Compact single line

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 1)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorSubtle)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colorInverted).
		Bold(true).
		Background(colorActive).
		Padding(0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorInverted).
		Background(colorActive)

	return d
}

// createProjectDelegate creates a custom delegate for project items
func createProjectDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.SetHeight(1) // Compact single line

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 1)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorSubtle)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colorInverted).
		Bold(true).
		Background(colorActive).
		Padding(0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorInverted).
		Background(colorActive)

	return d
}

// createLinkDelegate creates a custom delegate for link items
func createLinkDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.SetHeight(1) // Compact single line

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Bold(true).
		Padding(0, 1)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorSubtle)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colorInverted).
		Bold(true).
		Background(colorActive).
		Padding(0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(colorInverted).
		Background(colorActive)

	return d
}

// createNavigationDelegate creates a custom delegate for navigation items
func createNavigationDelegate() list.DefaultDelegate {
	d := list.NewDefaultDelegate()
	d.SetHeight(1) // Compact single line

	d.Styles.NormalTitle = lipgloss.NewStyle().
		Foreground(colorPrimary).
		Padding(0, 1)

	d.Styles.NormalDesc = lipgloss.NewStyle().
		Foreground(colorSubtle)

	d.Styles.SelectedTitle = lipgloss.NewStyle().
		Foreground(colorInverted).
		Background(colorActive).
		Bold(true).
		Padding(0, 1)

	d.Styles.SelectedDesc = lipgloss.NewStyle().
		Foreground(lipgloss.Color("234")).
		Background(colorActive)

	return d
}