package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
)

type toolsModel struct {
	tools    []admin.ToolInfo
	selected int
}

func newToolsModel() toolsModel { return toolsModel{} }

func (t toolsModel) withTools(list []admin.ToolInfo) toolsModel {
	// Sort by est_tokens desc, then by name asc.
	sorted := append([]admin.ToolInfo(nil), list...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].EstTokens != sorted[j].EstTokens {
			return sorted[i].EstTokens > sorted[j].EstTokens
		}
		return sorted[i].Name < sorted[j].Name
	})
	t.tools = sorted
	if t.selected >= len(sorted) {
		t.selected = 0
	}
	return t
}

func (t toolsModel) handleKey(m model, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "j", "down":
		if len(t.tools) > 0 {
			t.selected = (t.selected + 1) % len(t.tools)
		}
	case "k", "up":
		if len(t.tools) > 0 {
			t.selected = (t.selected - 1 + len(t.tools)) % len(t.tools)
		}
	}
	m.toolsView = t
	return m, nil
}

func (t toolsModel) view(m model) string {
	if len(t.tools) == 0 {
		return disabledStyle.Render("\n  (no tools — no servers running?)\n")
	}
	var b strings.Builder
	total := 0
	for _, tl := range t.tools {
		total += tl.EstTokens
	}
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %d tools — total ~%d tokens", len(t.tools), total)))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %8s  %-14s  %s", "~TOKENS", "SERVER", "TOOL")))
	b.WriteString("\n")
	show := t.tools
	// Reserve ~7 rows for header+footer+preview; show the top N when over budget.
	if m.h > 0 && len(show) > m.h-7 {
		show = show[:m.h-7]
	}
	for i, tl := range show {
		line := fmt.Sprintf("  %8d  %-14s  %s", tl.EstTokens, truncate(tl.Server, 14), tl.Name)
		if i == t.selected {
			line = lipgloss.NewStyle().Reverse(true).Render(line)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	if t.selected < len(t.tools) {
		cur := t.tools[t.selected]
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(fmt.Sprintf("  description: %s", truncate(cur.Description, 120))))
	}
	return b.String()
}
