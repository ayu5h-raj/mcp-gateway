package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

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
		if len(list) == 0 {
			s.selected = 0
		} else {
			s.selected = len(list) - 1
		}
	}
	return s
}

func (s serversModel) handleKey(m model, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "j", "down":
		if len(s.servers) > 0 {
			s.selected = (s.selected + 1) % len(s.servers)
		}
	case "k", "up":
		if len(s.servers) > 0 {
			s.selected = (s.selected - 1 + len(s.servers)) % len(s.servers)
		}
	case "enter":
		if len(s.servers) > 0 {
			m.detail = &detailModel{server: s.servers[s.selected].Name}
			m.serversView = s
			return m, nil
		}
	case "t":
		if len(s.servers) > 0 {
			srv := s.servers[s.selected]
			op := "enable"
			if srv.Enabled {
				op = "disable"
			}
			m.serversView = s
			return m, cmdAction(m.client, op, srv.Name)
		}
	case "r":
		if len(s.servers) > 0 {
			m.serversView = s
			return m, cmdAction(m.client, "restart", s.servers[s.selected].Name)
		}
	}
	m.serversView = s
	return m, nil
}

func (s serversModel) view(_ model) string {
	if len(s.servers) == 0 {
		return disabledStyle.Render("\n  (no servers configured — try `mcp-gateway add`)\n")
	}
	var b strings.Builder
	header := fmt.Sprintf("  %-14s %-10s %-12s %6s %8s %s",
		"NAME", "STATE", "PREFIX", "TOOLS", "~TOKENS", "LAST ACTIVITY")
	b.WriteString(headerStyle.Render(header))
	b.WriteString("\n")
	now := time.Now()
	for i, srv := range s.servers {
		age := "—"
		if !srv.StartedAt.IsZero() {
			age = shortDuration(now.Sub(srv.StartedAt)) + " ago"
		}
		row := fmt.Sprintf("  %-14s %s %-8s %-12s %6d %8d %s",
			truncate(srv.Name, 14), glyph(srv.State), padRight(srv.State, 8),
			truncate(srv.Prefix, 12), srv.ToolCount, srv.EstTokens, age)
		if i == s.selected {
			row = lipgloss.NewStyle().Reverse(true).Render(row)
		}
		b.WriteString(row)
		b.WriteString("\n")
	}
	return b.String()
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}
