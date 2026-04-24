package tui

import (
	"fmt"
	"strings"
	"time"

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
		return "\n" + disabledText.Render("(no servers configured — try `mcp-gateway add`)") + "\n"
	}

	// Column widths. Fixed budget for the rigid columns; NAME + PREFIX get
	// enough to read real server names; LAST fills the rest visually.
	const (
		nameW  = 18
		stateW = 12 // "restarting" is the longest state name
		prefW  = 14
		toolsW = 6
		tokenW = 9
		lastW  = 16
	)

	var b strings.Builder

	// Column header row — bold, dim grey, with a blank spacer column for the bar.
	b.WriteString(" ") // space for the bar column
	b.WriteString(colHeader.Render(fmt.Sprintf(
		" %-*s %-*s %-*s %*s %*s  %-*s",
		nameW, "NAME",
		stateW, "STATE",
		prefW, "PREFIX",
		toolsW, "TOOLS",
		tokenW, "~TOKENS",
		lastW, "LAST ACTIVITY",
	)))
	b.WriteString("\n")

	now := time.Now()
	for i, srv := range s.servers {
		age := "—"
		if !srv.StartedAt.IsZero() {
			age = shortDuration(now.Sub(srv.StartedAt)) + " ago"
		}
		// Glyph is pre-colored; stateText colors the state word.
		statusCol := glyph(srv.State) + " " + stateText(padRight(srv.State, stateW-2))

		rowPlain := fmt.Sprintf(
			" %-*s %s %-*s %*d %*d  %-*s",
			nameW, truncate(srv.Name, nameW),
			statusCol, // already styled + padded
			prefW, truncate(srv.Prefix, prefW),
			toolsW, srv.ToolCount,
			tokenW, srv.EstTokens,
			lastW, truncate(age, lastW),
		)

		bar := unselectedBar
		row := rowPlain
		if i == s.selected {
			bar = selectedBar
			row = selectedRow.Render(rowPlain)
		}
		b.WriteString(bar)
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
