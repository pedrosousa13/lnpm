package tui

import (
	"strings"
)

// buildPackageDetail builds the detail view for a package item
func (m Model) buildPackageDetail() string {
	if len(m.centerPanel.Items()) == 0 {
		return "No packages available"
	}

	selectedItem := m.centerPanel.SelectedItem()
	if selectedItem == nil {
		return "Select a package to view details..."
	}

	pkg, ok := selectedItem.(PackageItem)
	if !ok {
		return "Invalid package item"
	}

	var content strings.Builder

	// Title
	content.WriteString(titleStyle.Render("📦 " + pkg.Name))
	content.WriteString("\n\n")

	// Info section
	content.WriteString(detailLabelStyle.Render("● Package Information"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("Name:") + "        " + detailValueStyle.Render(pkg.Name) + "\n")
	content.WriteString("  " + statusKeyStyle.Render("Version:") + "     " + detailValueStyle.Render(pkg.Version) + "\n")
	content.WriteString("  " + statusKeyStyle.Render("Hash:") + "        " + detailValueStyle.Render(pkg.Hash) + "\n")
	content.WriteString("  " + statusKeyStyle.Render("Source:") + "      " + detailValueStyle.Render(pkg.SourcePath) + "\n\n")

	// Linked Projects section
	content.WriteString(detailLabelStyle.Render("● Linked Projects (" + itoa(pkg.LinkedCount) + ")"))
	content.WriteString("\n")

	if pkg.LinkedCount == 0 {
		content.WriteString(detailValueStyle.Render("  No linked projects"))
		content.WriteString("\n")
	} else {
		for _, projName := range pkg.LinkedProjects {
			content.WriteString("  " + detailValueStyle.Render("• "+projName) + "\n")
		}
	}
	content.WriteString("\n")

	// Actions section
	content.WriteString(detailLabelStyle.Render("● Available Actions"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("[p]") + " Push to all linked projects\n")
	content.WriteString("  " + statusKeyStyle.Render("[o]") + " Open source folder\n")
	content.WriteString("  " + statusKeyStyle.Render("[u]") + " Update in all projects\n")
	content.WriteString("  " + statusKeyStyle.Render("[r]") + " Remove all links\n")

	return content.String()
}

// buildProjectDetail builds the detail view for a project item
func (m Model) buildProjectDetail() string {
	if len(m.centerPanel.Items()) == 0 {
		return "No projects available"
	}

	selectedItem := m.centerPanel.SelectedItem()
	if selectedItem == nil {
		return "Select a project to view details..."
	}

	proj, ok := selectedItem.(item)
	if !ok {
		return "Invalid project item"
	}

	var content strings.Builder

	// Title
	content.WriteString(titleStyle.Render("📁 " + proj.Title()))
	content.WriteString("\n\n")

	// Info section
	content.WriteString(detailLabelStyle.Render("● Project Information"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("Path:") + "  " + detailValueStyle.Render(proj.Description()) + "\n\n")

	// Linked packages section (placeholder)
	content.WriteString(detailLabelStyle.Render("● Linked Packages"))
	content.WriteString("\n")
	content.WriteString("  " + detailValueStyle.Render("• @ef-global/backpack (v3.32.0)") + "\n\n")

	// Actions section
	content.WriteString(detailLabelStyle.Render("● Available Actions"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("[r]") + " Remove link from project\n")
	content.WriteString("  " + statusKeyStyle.Render("[Space]") + " Toggle for batch removal\n")

	return content.String()
}

// buildCurrentLinkDetail builds the detail view for a link item
func (m Model) buildCurrentLinkDetail() string {
	if len(m.centerPanel.Items()) == 0 {
		return "No linked packages in current project"
	}

	selectedItem := m.centerPanel.SelectedItem()
	if selectedItem == nil {
		return "Select a link to view details..."
	}

	link, ok := selectedItem.(LinkItem)
	if !ok {
		return "Invalid link item"
	}

	var content strings.Builder

	// Title
	content.WriteString(titleStyle.Render("🔗 " + link.Name))
	content.WriteString("\n\n")

	// Info section
	content.WriteString(detailLabelStyle.Render("● Link Information"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("Name:") + "          " + detailValueStyle.Render(link.Name) + "\n")
	content.WriteString("  " + statusKeyStyle.Render("Current Ver:") + "  " + detailValueStyle.Render(link.Version) + "\n")
	if link.OriginalVersion != "" && link.OriginalVersion != link.Version {
		content.WriteString("  " + statusKeyStyle.Render("Original Ver:") + " " + detailValueStyle.Render(link.OriginalVersion) + "\n")
	}
	content.WriteString("  " + statusKeyStyle.Render("Linked At:") + "    " + detailValueStyle.Render(formatTime(link.LinkedAt)) + "\n")
	content.WriteString("  " + statusKeyStyle.Render("Source:") + "      " + detailValueStyle.Render(link.SourcePath) + "\n\n")

	// Actions section
	content.WriteString(detailLabelStyle.Render("● Available Actions"))
	content.WriteString("\n")
	content.WriteString("  " + statusKeyStyle.Render("[r]") + " Remove this link\n")
	content.WriteString("  " + statusKeyStyle.Render("[p]") + " Push update to project\n")

	return content.String()
}

// Helper functions
func itoa(n int) string {
	if n < 0 {
		return ""
	}
	if n == 0 {
		return "0"
	}
	var result string
	for n > 0 {
		result = string(rune('0' + n % 10)) + result
		n /= 10
	}
	return result
}

func formatTime(t interface{}) string {
	// Basic formatter - can be improved
	return "Just now"
}

// updateRightPanel updates the right panel content based on current selection
func (m *Model) updateRightPanel() {
	switch m.currentView {
	case 0: // Packages
		m.rightPanel = m.buildPackageDetail()
	case 1: // Projects
		m.rightPanel = m.buildProjectDetail()
	case 2: // Current Links
		m.rightPanel = m.buildCurrentLinkDetail()
	default:
		m.rightPanel = "Select an item to view details..."
	}
}
