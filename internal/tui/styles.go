package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Color scheme - inspired by gh-dash with better contrast and clarity
var (
	// Text colors
	colorPrimary   = lipgloss.Color("255")   // White
	colorSecondary = lipgloss.Color("251")   // Light gray (c6c6c6)
	colorSubtle    = lipgloss.Color("245")   // Gray (8a8a8a)
	colorFaint     = lipgloss.Color("8")     // Darker gray

	// Backgrounds and selections
	colorBg        = lipgloss.Color("0")     // Black (terminal default)
	colorSelected  = lipgloss.Color("8")     // Dark gray (#808080)
	colorInverted  = lipgloss.Color("0")     // Dark background

	// Borders
	colorBorder    = lipgloss.Color("8")     // Medium gray
	colorBorderFaint = lipgloss.Color("0")   // Dark border

	// Status colors
	colorSuccess   = lipgloss.Color("2")     // Green (#008000)
	colorWarning   = lipgloss.Color("1")     // Red (#800000)
	colorError     = lipgloss.Color("1")     // Red
	colorActive    = lipgloss.Color("6")     // Cyan (accent)
	colorFocus     = lipgloss.Color("6")     // Cyan
)

// Styles
var (
	// Panel styles - minimal padding like gh-dash
	panelStyle = lipgloss.NewStyle().
		Padding(0, 0).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorBorder)

	panelFocusedStyle = lipgloss.NewStyle().
		Padding(0, 0).
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(colorActive).
		Bold(false)

	// Title styles
	titleStyle = lipgloss.NewStyle().
		Foreground(colorSubtle).
		Bold(false).
		Padding(0, 1)

	titleFocusedStyle = lipgloss.NewStyle().
		Foreground(colorActive).
		Bold(true).
		Padding(0, 1)

	// List item styles
	itemStyle = lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(colorPrimary)

	itemSelectedStyle = lipgloss.NewStyle().
		Padding(0, 1).
		Foreground(colorInverted).
		Background(colorActive).
		Bold(true)

	// Status bar styles
	statusBarStyle = lipgloss.NewStyle().
		Padding(0, 1).
		Background(colorBg).
		Foreground(colorPrimary)

	statusKeyStyle = lipgloss.NewStyle().
		Foreground(colorActive).
		Bold(true)

	statusDescStyle = lipgloss.NewStyle().
		Foreground(colorSubtle)

	// Detail panel styles
	detailLabelStyle = lipgloss.NewStyle().
		Foreground(colorActive).
		Bold(true)

	detailValueStyle = lipgloss.NewStyle().
		Foreground(colorSecondary)

	// Help text styles
	helpKeyStyle = lipgloss.NewStyle().
		Foreground(colorActive).
		Bold(true)

	helpDescStyle = lipgloss.NewStyle().
		Foreground(colorSubtle)
)

// StyledPanel returns a styled panel with optional focus state
func StyledPanel(content string, title string, focused bool) string {
	style := panelStyle
	titleStyle := titleStyle
	if focused {
		style = panelFocusedStyle
		titleStyle = titleFocusedStyle
	}

	if title != "" {
		titleStyle = titleStyle.Padding(0, 1)
		content = lipgloss.JoinVertical(
			lipgloss.Left,
			titleStyle.Render(title),
			content,
		)
	}

	return style.Render(content)
}

// DetailPair returns a styled key-value pair for the detail panel
func DetailPair(label, value string) string {
	labelStr := detailLabelStyle.Render(label)
	valueStr := detailValueStyle.Render(value)
	return lipgloss.JoinHorizontal(lipgloss.Top, labelStr, detailValueStyle.Render(": "), valueStr)
}

// StatusBar returns a styled status bar with key-value pairs
func StatusBar(items []StatusItem, width int) string {
	var content string
	for _, item := range items {
		key := statusKeyStyle.Render(item.Key)
		desc := statusDescStyle.Render(item.Desc)
		content += key + " " + desc + "  "
	}

	bar := statusBarStyle.Width(width).Render(content)
	return bar
}

// StatusItem represents a single item in the status bar
type StatusItem struct {
	Key  string
	Desc string
}

// ListItemStyle returns a styled list item
func ListItemStyle(content string, selected bool) string {
	if selected {
		return itemSelectedStyle.Render(content)
	}
	return itemStyle.Render(content)
}

// CenterText centers text within the given width
func CenterText(text string, width int) string {
	return lipgloss.NewStyle().
		Width(width).
		Align(lipgloss.Center).
		Render(text)
}

// PadText adds padding to text
func PadText(text string, top, right, bottom, left int) string {
	return lipgloss.NewStyle().
		Padding(top, right, bottom, left).
		Render(text)
}
