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

	// First pass: decide whether a bottom description footer is needed so we
	// can budget its height into pageSize. We need the footer only when the
	// terminal is too narrow to fit inline descriptions on each row. Use the
	// minimum possible nameW (20) to make a conservative descW guess.
	minNameW := 20
	guessDescW := m.w - 6 - (1 + tokenW + 2 + serverW + 2 + minNameW + 3)
	wantFooter := guessDescW < 10

	// Chrome: header(1) + panel top border(1) + summary(1) + col header(1) +
	// panel bottom border(1) + gap(1) + statusline(1) = 7 rows.
	// + 2 extra when the bottom description footer is present (blank + desc).
	chrome := 7
	if wantFooter {
		chrome += 2
	}
	pageSize := m.h - chrome
	if pageSize < 3 {
		// Either m.h hasn't arrived yet (0) or terminal is tiny. Keep the
		// viewport tight so windowing still works; content will clip at the
		// bottom but scrolling stays correct.
		pageSize = 3
	}
	start, end := windowAround(len(t.tools), t.selected, pageSize)

	// Figure out the widest tool name in the visible window so names align
	// into a tidy column without a hardcoded width cap.
	nameW := minNameW
	for i := start; i < end; i++ {
		if l := len(t.tools[i].Name); l > nameW {
			nameW = l
		}
	}

	// Recompute descW with the actual nameW.
	descW := m.w - 6 - (1 + tokenW + 2 + serverW + 2 + nameW + 3)
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
	// Bottom description footer only when inline descriptions aren't showing.
	// Truncate to terminal width so it never wraps and breaks the height budget.
	if wantFooter && t.selected < len(t.tools) {
		cur := t.tools[t.selected]
		maxDesc := m.w - 18 // "description: " + panel chrome
		if maxDesc < 20 {
			maxDesc = 20
		}
		b.WriteString("\n ")
		b.WriteString(mutedText.Render("description: " + truncate(strings.ReplaceAll(cur.Description, "\n", " "), maxDesc)))
	}
	return b.String()
}
