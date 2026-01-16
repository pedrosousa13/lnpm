package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Data loading messages
type packagesLoadedMsg struct {
	items []list.Item
	err   error
}

type projectsLoadedMsg struct {
	items []list.Item
	err   error
}

type currentLinksLoadedMsg struct {
	items []list.Item
	err   error
}

// Model represents the TUI application state
type Model struct {
	// Panels
	leftPanel   list.Model
	centerPanel list.Model
	rightPanel  string // For now, just a string

	// Layout
	width  int
	height int

	// State
	activePanel      int               // 0=list, 1=detail (now only 2 panels)
	currentView      int               // 0=packages, 1=projects, 2=current
	quitting         bool
	loading          bool
	modal            *Modal            // Current modal dialog
	pendingAction    string            // Action waiting for confirmation
	pendingItem      interface{}       // Item associated with pending action
	selectionManager *SelectionManager // Track selected items for batch operations
}

// NewModel creates a new TUI model
func NewModel() Model {
	// Initialize panels
	leftItems := []list.Item{
		item{title: "Packages", desc: "Manage published packages"},
		item{title: "Projects", desc: "View linked projects"},
		item{title: "Current", desc: "Current project links"},
	}

	leftDelegate := createNavigationDelegate()
	leftList := list.New(leftItems, leftDelegate, 0, 0)
	leftList.Title = "Navigation"
	leftList.SetShowStatusBar(false)
	leftList.SetFilteringEnabled(false)
	leftList.SetShowHelp(false)

	// Start with empty center panel
	centerItems := []list.Item{}
	centerDelegate := createPackageDelegate() // Default to package styling
	centerList := list.New(centerItems, centerDelegate, 0, 0)
	centerList.Title = "⏳ Loading packages..."
	centerList.SetShowStatusBar(false)
	centerList.SetFilteringEnabled(false)
	centerList.SetShowHelp(false)

	return Model{
		leftPanel:        leftList,
		centerPanel:      centerList,
		rightPanel:       "⏳ Loading packages...",
		activePanel:      1, // Start with center panel active
		currentView:      0, // Start with packages view
		loading:          true,
		modal:            nil,
		selectionManager: NewSelectionManager(),
	}
}

// item represents a list item
type item struct {
	title, desc string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return loadPackagesCmd()
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	// Handle modal input first
	if m.modal != nil {
		shouldClose, modalCmd := m.modal.Update(msg)
		if shouldClose {
			if m.pendingAction != "" {
				// Execute the pending action if confirmed
				if m.modal.Type == ModalConfirm && m.modal.GetSelectedOption() == 0 {
					// User clicked "Yes"
					return m.executePendingAction()
				}
			}
			// Close modal
			m.modal = nil
			m.pendingAction = ""
			m.pendingItem = nil
		}
		return m, modalCmd
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resizePanels()
		if m.modal != nil {
			m.modal.Width = m.width
			m.modal.Height = m.height
		}

	case packagesLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.centerPanel.Title = "Error loading packages"
			m.rightPanel = fmt.Sprintf("❌ Error: %v", msg.err)
		} else {
			m.centerPanel.SetItems(msg.items)
			m.centerPanel.SetDelegate(createPackageDelegate())
			m.centerPanel.Title = "📦 Packages"
			m.updateRightPanel()
		}

	// Projects view removed

	case currentLinksLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.centerPanel.Title = "Error loading links"
			m.rightPanel = fmt.Sprintf("❌ Error: %v", msg.err)
		} else {
			m.centerPanel.SetItems(msg.items)
			m.centerPanel.SetDelegate(createLinkDelegate())
			m.centerPanel.Title = "🔗 Current Project Links"
			m.updateRightPanel()
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		// Tab switching with left/right arrows or h/l
		case "left", "h":
			m.currentView = (m.currentView - 1 + 2) % 2 // Modulo 2 since we only have 2 tabs now
			switch m.currentView {
			case 0:
				return m.switchToPackages()
			case 1:
				return m.switchToCurrentLinks()
			}

		case "right", "l":
			m.currentView = (m.currentView + 1) % 2 // Modulo 2 since we only have 2 tabs now
			switch m.currentView {
			case 0:
				return m.switchToPackages()
			case 1:
				return m.switchToCurrentLinks()
			}

		// Direct tab selection with number keys
		case "1":
			m.currentView = 0
			return m.switchToPackages()

		case "2":
			m.currentView = 1
			return m.switchToCurrentLinks()

		// Tab between list and detail panels
		case "tab":
			m.activePanel = (m.activePanel + 1) % 2

		case "j", "down":
			if m.activePanel == 0 && !m.centerPanel.FilteringEnabled() {
				m.centerPanel, cmd = m.centerPanel.Update(msg)
				m.updateRightPanel() // Update detail view when selection changes
			}

		case "k", "up":
			if m.activePanel == 0 && !m.centerPanel.FilteringEnabled() {
				m.centerPanel, cmd = m.centerPanel.Update(msg)
				m.updateRightPanel() // Update detail view when selection changes
			}

		case "/":
			if m.activePanel == 0 { // Only allow filtering in list panel
				return m.enterFilterMode()
			}

		// Action handlers
		case "r": // Remove/unlink
			if m.currentView == 2 { // Only on current links view
				return m.showRemoveConfirmation()
			}

		case "p": // Push to projects
			if m.currentView == 0 { // Only on packages view
				return m.handlePushAction()
			}

		case "o": // Open source folder
			if m.currentView == 0 { // Only on packages view
				return m.handleOpenAction()
			}

		case "u": // Update package
			if m.currentView == 0 { // Only on packages view
				return m.handleUpdateAction()
			}

		case "?": // Help
			m.modal = NewHelpModal()
			m.modal.Width = m.width
			m.modal.Height = m.height
			return m, nil

		case "ctrl+r": // Refresh
			// Reload current view
			m.loading = true
			switch m.currentView {
			case 0:
				return m, loadPackagesCmd()
			case 1:
				return m, loadCurrentLinksCmd()
			}

		case " ": // Space - toggle selection
			if m.activePanel == 0 { // In list panel
				selectedIdx := m.centerPanel.Index()
				m.selectionManager.Toggle(selectedIdx)
				m.updateRightPanel()
			}

		default:
			// Handle filter input if filter is active
			if m.centerPanel.FilteringEnabled() {
				return m.handleFilterInput(msg)
			}
		}
	}

	return m, cmd
}

// View renders the TUI
func (m Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	// If modal is active, show it overlaid on top
	if m.modal != nil {
		m.modal.Width = m.width
		m.modal.Height = m.height
		return m.modal.View()
	}

	// Calculate panel widths (60%/40% for list and details)
	listWidth := m.width * 60 / 100
	detailWidth := m.width - listWidth

	// Render header
	header := m.renderHeader()

	// Render tab bar
	tabs := m.renderTabs()

	// Render list panel (left side, full height)
	// centerPanel matches listWidth-2 content size set in resizePanels
	listContent := m.centerPanel.View()
	listPanel := StyledPanel(listContent, "", m.activePanel == 0)

	// Render detail panel (right side, full height)
	// Wrap content to fit detailWidth-2 (accounting for borders)
	detailContent := lipgloss.NewStyle().
		Width(detailWidth - 2).
		Height(m.height - 7). // -7 to account for overhead(5) + borders(2)
		Render(m.rightPanel)

	detailPanel := StyledPanel(detailContent, "", m.activePanel == 1)

	// Combine list and detail panels horizontally
	panels := lipgloss.JoinHorizontal(lipgloss.Top, listPanel, detailPanel)

	// Add hints bar and status bar at the bottom
	hintsBar := m.renderHintsBar()
	statusBar := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Top, header, tabs, panels, hintsBar, statusBar)
}

// getCenterPanelTitle returns the title for the center panel
func (m Model) getCenterPanelTitle() string {
	titles := []string{"📦 Packages", "� Current Project"}
	if m.currentView < len(titles) {
		return titles[m.currentView]
	}
	return "Loading..."
}

// renderTabs renders the view switcher tabs at the top
func (m Model) renderTabs() string {
	views := []string{"Packages", "Current Project"}

	var tabs []string
	for i, view := range views {
		style := lipgloss.NewStyle().
			Padding(0, 2).
			Foreground(colorSubtle)

		if i == m.currentView {
			style = style.
				Background(colorActive).
				Foreground(colorInverted).
				Bold(true).
				PaddingBottom(1) // Match height of inactive tabs (which have a bottom border)
		} else {
			// Add a subtle bottom border to inactive tabs to make them look connected
			style = style.
				Border(lipgloss.Border{Bottom: "─"}).
				BorderForeground(colorBorder)
		}

		tabs = append(tabs, style.Render(view))
	}

	// This gap fills the space between tabs and the right edge
	// It completes the "border line" under the tabs
	gap := lipgloss.NewStyle().
		Border(lipgloss.Border{Bottom: "─"}).
		BorderForeground(colorBorder).
		Width(m.width - lipgloss.Width(lipgloss.JoinHorizontal(lipgloss.Top, tabs...)) - 2). // -2 for padding
		Render("")

	tabBar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	tabBar = lipgloss.JoinHorizontal(lipgloss.Bottom, tabBar, gap)

	return lipgloss.NewStyle().
		Padding(0, 1). // Outer padding matching other elements
		Render(tabBar)
}

// renderHeader renders the app header with title and quick info
func (m Model) renderHeader() string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(colorActive).
		Padding(0, 1).
		Render("🔗 lnpm")

	return lipgloss.NewStyle().
		Background(colorInverted).
		Width(m.width).
		Render(title)
}

// renderHintsBar renders context-sensitive hints based on current view
func (m Model) renderHintsBar() string {
	var hints string

	switch m.activePanel {
	case 0: // Navigation (List/Main)
		switch m.currentView {
		case 0: // Packages view
			hints = "↑↓ select • p push • o open • r remove • u update • space batch"
		case 1: // Current links view
			hints = "↑↓ select • r remove • p push • space batch"
		}
	case 1: // Details
		hints = "← back"
	}

	return lipgloss.NewStyle().
		Foreground(colorSubtle).
		Padding(0, 1).
		Background(colorInverted).
		Width(m.width).
		Render("  " + hints)
}

// renderStatusBar renders the status bar at the bottom
func (m Model) renderStatusBar() string {
	panelNames := []string{"Navigation", "Main List", "Details"}
	viewNames := []string{"Packages", "Projects", "Current Links"}

	loadingStatus := ""
	if m.loading {
		loadingStatus = "⏳ "
	}

	selectionStatus := m.selectionManager.RenderSelectionStatus()
	if selectionStatus != "" {
		selectionStatus = " • " + selectionStatus
	}

	statusItems := []string{
		statusKeyStyle.Render("Panel:") + " " + statusDescStyle.Render(panelNames[m.activePanel]),
		statusKeyStyle.Render("View:") + " " + statusDescStyle.Render(viewNames[m.currentView]),
		loadingStatus + statusKeyStyle.Render("Help:") + " " + statusDescStyle.Render("?") + selectionStatus,
	}

	// Join with explicit spacing
	content := strings.Join(statusItems, "   ")

	status := lipgloss.NewStyle().
		Background(colorInverted).
		Foreground(colorPrimary).
		Padding(0, 1).
		Width(m.width).
		Render(content)

	return status
}

// resizePanels adjusts panel sizes when window resizes
func (m *Model) resizePanels() {
	listWidth := m.width * 60 / 100
	// Detail panel takes the remaining width

	// -5 accounts for overhead: header(1), tabs(2), hints(1), status(1)
	// -2 accounts for panel borders (top/bottom)
	// Total deduction: 7
	m.centerPanel.SetSize(listWidth-2, m.height-7)
}

// loadPackagesCmd loads packages data
func loadPackagesCmd() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		items, err := GetPackagesList()
		return packagesLoadedMsg{items: items, err: err}
	})
}

// loadProjectsCmd loads projects data
func loadProjectsCmd() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		items, err := GetProjectsList()
		return projectsLoadedMsg{items: items, err: err}
	})
}

// loadCurrentLinksCmd loads current project links
func loadCurrentLinksCmd() tea.Cmd {
	return tea.Cmd(func() tea.Msg {
		items, err := GetCurrentProjectLinks()
		return currentLinksLoadedMsg{items: items, err: err}
	})
}

// handleRemoveAction handles removing a link from the current project
func (m *Model) handleRemoveAction() (tea.Model, tea.Cmd) {
	selected := m.centerPanel.SelectedItem()
	if selected == nil {
		return m, nil
	}

	linkItem, ok := selected.(LinkItem)
	if !ok {
		return m, nil
	}

	return m, func() tea.Msg {
		err := ExecuteAction(ActionRemove, linkItem)
		if err != nil {
			m.rightPanel = fmt.Sprintf("❌ Error removing link: %v", err)
		} else {
			m.rightPanel = fmt.Sprintf("✅ Successfully removed %s\nRefresh to see changes (r key)", linkItem.Name)
			// Reload the current links
			return loadCurrentLinksCmd()
		}
		return nil
	}
}

// handlePushAction handles pushing a package to its linked projects
func (m *Model) handlePushAction() (tea.Model, tea.Cmd) {
	selected := m.centerPanel.SelectedItem()
	if selected == nil {
		return m, nil
	}

	pkgItem, ok := selected.(PackageItem)
	if !ok {
		return m, nil
	}

	return m, func() tea.Msg {
		err := ExecuteAction(ActionPush, pkgItem)
		if err != nil {
			m.rightPanel = fmt.Sprintf("❌ Error pushing package: %v", err)
		} else {
			m.rightPanel = fmt.Sprintf("✅ Successfully pushed %s to all linked projects", pkgItem.Name)
		}
		return nil
	}
}

// handleOpenAction handles opening a package's source folder
func (m *Model) handleOpenAction() (tea.Model, tea.Cmd) {
	selected := m.centerPanel.SelectedItem()
	if selected == nil {
		return m, nil
	}

	pkgItem, ok := selected.(PackageItem)
	if !ok {
		return m, nil
	}

	return m, func() tea.Msg {
		err := ExecuteAction(ActionOpen, pkgItem)
		if err != nil {
			m.rightPanel = fmt.Sprintf("❌ Error opening source: %v", err)
		} else {
			m.rightPanel = fmt.Sprintf("✅ Opened %s source folder", pkgItem.Name)
		}
		return nil
	}
}

// handleUpdateAction handles updating a package
func (m *Model) handleUpdateAction() (tea.Model, tea.Cmd) {
	selected := m.centerPanel.SelectedItem()
	if selected == nil {
		return m, nil
	}

	pkgItem, ok := selected.(PackageItem)
	if !ok {
		return m, nil
	}

	return m, func() tea.Msg {
		err := ExecuteAction(ActionUpdate, pkgItem)
		if err != nil {
			m.rightPanel = fmt.Sprintf("❌ Error updating package: %v", err)
		} else {
			m.rightPanel = fmt.Sprintf("✅ Successfully updated %s in all linked projects", pkgItem.Name)
		}
		return nil
	}
}

// showRemoveConfirmation shows a confirmation dialog before removing a link
func (m *Model) showRemoveConfirmation() (tea.Model, tea.Cmd) {
	selected := m.centerPanel.SelectedItem()
	if selected == nil {
		return m, nil
	}

	linkItem, ok := selected.(LinkItem)
	if !ok {
		return m, nil
	}

	// Store the pending action and item
	m.pendingAction = "remove"
	m.pendingItem = linkItem

	// Create confirmation modal
	message := fmt.Sprintf("Remove link for %s v%s from current project?", linkItem.Name, linkItem.Version)
	m.modal = NewConfirmModal("Remove Link", message, []string{"Yes", "No"})
	m.modal.Width = m.width
	m.modal.Height = m.height

	return m, nil
}

// executePendingAction executes the action that was confirmed
func (m *Model) executePendingAction() (tea.Model, tea.Cmd) {
	switch m.pendingAction {
	case "remove":
		if linkItem, ok := m.pendingItem.(LinkItem); ok {
			return m, func() tea.Msg {
				err := ExecuteAction(ActionRemove, linkItem)
				if err != nil {
					m.rightPanel = fmt.Sprintf("❌ Error removing link: %v", err)
				} else {
					m.rightPanel = fmt.Sprintf("✅ Successfully removed %s\nRefresh to see changes (r key)", linkItem.Name)
					// Reload the current links
					return loadCurrentLinksCmd()
				}
				return nil
			}
		}
	}
	return m, nil
}