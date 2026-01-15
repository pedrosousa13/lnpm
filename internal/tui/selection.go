package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// SelectionManager tracks which items are selected for batch operations
type SelectionManager struct {
	selected map[int]bool // Map of selected indices to true
}

// NewSelectionManager creates a new selection manager
func NewSelectionManager() *SelectionManager {
	return &SelectionManager{
		selected: make(map[int]bool),
	}
}

// Toggle toggles the selection of an item at the given index
func (s *SelectionManager) Toggle(index int) {
	if s.selected[index] {
		delete(s.selected, index)
	} else {
		s.selected[index] = true
	}
}

// IsSelected checks if an item is selected
func (s *SelectionManager) IsSelected(index int) bool {
	return s.selected[index]
}

// Clear clears all selections
func (s *SelectionManager) Clear() {
	s.selected = make(map[int]bool)
}

// Count returns the number of selected items
func (s *SelectionManager) Count() int {
	return len(s.selected)
}

// Selected returns a slice of selected indices
func (s *SelectionManager) Selected() []int {
	var indices []int
	for i := range s.selected {
		indices = append(indices, i)
	}
	return indices
}

// RenderSelectionStatus renders a status string showing selection count
func (s *SelectionManager) RenderSelectionStatus() string {
	count := s.Count()
	if count == 0 {
		return ""
	}

	return lipgloss.NewStyle().
		Foreground(colorSecondary).
		Render(fmt.Sprintf("📌 %d selected", count))
}

// BatchRemoveFromLinks performs batch removal of links
func (s *SelectionManager) BatchRemoveFromLinks(items []LinkItem) error {
	if len(items) == 0 {
		return fmt.Errorf("no items available")
	}

	manager := NewBatchUnlinkManager()

	for idx := range s.selected {
		if idx < len(items) {
			manager.AddRemoval(items[idx].Name)
		}
	}

	err := manager.Execute()
	s.Clear()
	return err
}
