package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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
		return "\n" + disabledText.Render("(no tools — no servers running?)") + "\n"
	}
	var b strings.Builder

	total := 0
	for _, tl := range t.tools {
		total += tl.EstTokens
	}
	summary := fmt.Sprintf("%d tools  ·  total ~%d tokens",
		len(t.tools), total)
	b.WriteString(" ")
	b.WriteString(accentText.Render(summary))
	b.WriteString("\n")

	const (
		tokenW  = 9
		serverW = 14
	)
	b.WriteString(" ")
	b.WriteString(colHeader.Render(fmt.Sprintf(
		" %*s  %-*s  %s",
		tokenW, "~TOKENS",
		serverW, "SERVER",
		"TOOL",
	)))
	b.WriteString("\n")

	// Window around the selected row so the viewport follows navigation.
	pageSize := m.h - 9 // summary + col header + borders + description + statusline
	if pageSize < 1 {
		pageSize = len(t.tools)
	}
	start, end := windowAround(len(t.tools), t.selected, pageSize)

	// Figure out the widest tool name in the visible window so names align
	// into a tidy column without a hardcoded width cap.
	nameW := 0
	for i := start; i < end; i++ {
		if l := len(t.tools[i].Name); l > nameW {
			nameW = l
		}
	}
	if nameW < 20 {
		nameW = 20
	}

	// Reserve the remaining terminal width for an inline description column.
	// Panel chrome: 2 border + 2 padding + 2 for the bar+space prefix.
	descW := m.w - 6 - (1 + tokenW + 2 + serverW + 2 + nameW + 3) // "  —  " = 5 chars approx
	if descW < 10 {
		descW = 0
	}

	for i := start; i < end; i++ {
		tl := t.tools[i]
		name := padRight(tl.Name, nameW)
		base := fmt.Sprintf(" %*d  %-*s  %s",
			tokenW, tl.EstTokens,
			serverW, truncate(tl.Server, serverW),
			name,
		)
		var line string
		if descW > 0 && tl.Description != "" {
			descTxt := truncate(strings.ReplaceAll(tl.Description, "\n", " "), descW)
			line = base + "  " + mutedText.Render("— "+descTxt)
		} else {
			line = base
		}
		bar := unselectedBar
		if i == t.selected {
			bar = selectedBar
			// Apply selectedRow only to the columns; keep the description muted.
			if descW > 0 && tl.Description != "" {
				descTxt := truncate(strings.ReplaceAll(tl.Description, "\n", " "), descW)
				line = selectedRow.Render(base) + "  " + mutedText.Render("— "+descTxt)
			} else {
				line = selectedRow.Render(base)
			}
		}
		b.WriteString(bar)
		b.WriteString(line)
		b.WriteString("\n")
	}
	if t.selected < len(t.tools) {
		cur := t.tools[t.selected]
		b.WriteString("\n ")
		b.WriteString(mutedText.Render("description: " + truncate(cur.Description, 120)))
	}
	return b.String()
}
