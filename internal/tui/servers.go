// servers.go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
)

type serversModel struct {
	servers  []admin.ServerInfo
	selected int
}

func newServersModel() serversModel { return serversModel{} }

func (s serversModel) withServers(list []admin.ServerInfo) serversModel {
	s.servers = list
	if s.selected >= len(list) {
		s.selected = 0
	}
	return s
}

func (s serversModel) handleKey(m model, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	return m, nil // Phase 2 implements j/k/enter/r/t
}

func (s serversModel) view(m model) string {
	if len(s.servers) == 0 {
		return disabledStyle.Render("  (no servers configured — try `mcp-gateway add`)")
	}
	return "  servers view coming in phase 2"
}
