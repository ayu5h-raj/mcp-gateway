// tools.go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
)

type toolsModel struct {
	tools []admin.ToolInfo
}

func newToolsModel() toolsModel { return toolsModel{} }

func (t toolsModel) withTools(list []admin.ToolInfo) toolsModel {
	t.tools = list
	return t
}

func (t toolsModel) handleKey(m model, _ tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
func (t toolsModel) view(_ model) string                                  { return "  tools view coming in phase 4" }
