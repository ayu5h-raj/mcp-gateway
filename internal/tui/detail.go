package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
)

// detailModel shows one server's full info + its tools.
type detailModel struct {
	server string // server name
}

func (d *detailModel) handleKey(m model, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "r":
		return m, cmdAction(m.client, "restart", d.server)
	case "t":
		// Toggle direction depends on the server's current enabled state.
		for _, s := range m.servers {
			if s.Name == d.server {
				op := "enable"
				if s.Enabled {
					op = "disable"
				}
				return m, cmdAction(m.client, op, d.server)
			}
		}
	}
	return m, nil
}

func (d *detailModel) view(m model) string {
	var s *admin.ServerInfo
	for i := range m.servers {
		if m.servers[i].Name == d.server {
			s = &m.servers[i]
			break
		}
	}
	if s == nil {
		return errorStyle.Render(fmt.Sprintf("\n  server %q not found\n", d.server))
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %s  %s  (%s)", s.Name, glyph(s.State), s.State)))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("  prefix       : %s\n", s.Prefix))
	b.WriteString(fmt.Sprintf("  enabled      : %v\n", s.Enabled))
	b.WriteString(fmt.Sprintf("  restart_count: %d\n", s.RestartCount))
	if !s.StartedAt.IsZero() {
		b.WriteString(fmt.Sprintf("  started_at   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05")))
	}
	b.WriteString(fmt.Sprintf("  log_path     : %s\n", s.LogPath))
	b.WriteString(fmt.Sprintf("  tool_count   : %d  (~%d tokens)\n", s.ToolCount, s.EstTokens))
	if s.LastError != "" {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render("  last error: " + s.LastError))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render("  tools:"))
	b.WriteString("\n")
	count := 0
	for _, tl := range m.tools {
		if tl.Server != s.Prefix {
			continue
		}
		b.WriteString(fmt.Sprintf("    %8d  %s\n", tl.EstTokens, tl.Name))
		count++
		if count >= 20 {
			b.WriteString(helpStyle.Render(fmt.Sprintf("    … and %d more\n", s.ToolCount-count)))
			break
		}
	}
	return b.String()
}
