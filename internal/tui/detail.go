// detail.go
package tui

import tea "github.com/charmbracelet/bubbletea"

type detailModel struct {
	server string
}

func (d *detailModel) handleKey(m model, _ tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
func (d *detailModel) view(_ model) string                                   { return "  detail view coming in phase 5" }
