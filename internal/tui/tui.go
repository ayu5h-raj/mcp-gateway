// Package tui is a Bubble Tea TUI for mcp-gateway. k9s-style: three tabs
// (Servers, Events, Tools) driven by periodic /admin/{status,servers,tools}
// polling and an SSE subscription to /admin/events.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

// tab enumerates the top-level views.
type tab int

const (
	tabServers tab = iota
	tabEvents
	tabTools
	numTabs
)

func (t tab) String() string {
	switch t {
	case tabServers:
		return "Servers"
	case tabEvents:
		return "Events"
	case tabTools:
		return "Tools"
	}
	return "?"
}

// model is the root Bubble Tea model.
type model struct {
	client *adminclient.Client
	sock   string

	w, h      int
	activeTab tab
	connected bool
	lastErr   error

	status  admin.Status
	servers []admin.ServerInfo
	tools   []admin.ToolInfo

	serversView serversModel
	eventsView  eventsModel
	toolsView   toolsModel
	detail      *detailModel // nil = not in detail view
}

// Run is the entry point called from the cobra subcommand.
func Run(c *adminclient.Client, sock string) error {
	m := model{
		client:      c,
		sock:        sock,
		serversView: newServersModel(),
		eventsView:  newEventsModel(),
		toolsView:   newToolsModel(),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}

// Init kicks off polling and the SSE subscriber.
func (m model) Init() tea.Cmd {
	return tea.Batch(
		tick(),
		cmdPoll(m.client),
		// SSE subscriber is started in Phase 3.
	)
}

// Update routes messages. Precedence: quit keys > tab switch > detail view
// (if active) > active tab.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
	case tickMsg:
		return m, tea.Batch(tick(), cmdPoll(m.client))
	case statusMsg:
		if msg.Err != nil {
			m.connected = false
			m.lastErr = msg.Err
		} else {
			m.connected = true
			m.status = msg.Status
			m.lastErr = nil
		}
		return m, nil
	case serversMsg:
		if msg.Err == nil {
			m.servers = msg.Servers
			m.serversView = m.serversView.withServers(msg.Servers)
		}
		return m, nil
	case toolsMsg:
		if msg.Err == nil {
			m.tools = msg.Tools
			m.toolsView = m.toolsView.withTools(msg.Tools)
		}
		return m, nil
	case actionResultMsg:
		if msg.Err != nil {
			m.lastErr = fmt.Errorf("%s %s: %w", msg.Op, msg.Tgt, msg.Err)
		}
		return m, cmdPoll(m.client) // refresh immediately
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	// Forward to active sub-model for any other msg (e.g. eventMsg → events tab).
	switch m.activeTab {
	case tabEvents:
		newView, cmd := m.eventsView.Update(msg)
		m.eventsView = newView
		return m, cmd
	}
	return m, nil
}

func (m model) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Global keys first.
	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc":
		if m.detail != nil {
			m.detail = nil
			return m, nil
		}
	case "1":
		m.activeTab = tabServers
		return m, nil
	case "2":
		m.activeTab = tabEvents
		return m, nil
	case "3":
		m.activeTab = tabTools
		return m, nil
	}
	// Detail view takes precedence over tabs.
	if m.detail != nil {
		return m.detail.handleKey(m, k)
	}
	// Tab-specific.
	switch m.activeTab {
	case tabServers:
		return m.serversView.handleKey(m, k)
	case tabEvents:
		newView, cmd := m.eventsView.Update(k)
		m.eventsView = newView
		return m, cmd
	case tabTools:
		return m.toolsView.handleKey(m, k)
	}
	return m, nil
}

// View renders: header, tab bar, active tab (or detail if present), help footer.
func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.renderHeader())
	b.WriteString("\n")
	b.WriteString(m.renderTabBar())
	b.WriteString("\n")
	if m.detail != nil {
		b.WriteString(m.detail.view(m))
	} else {
		switch m.activeTab {
		case tabServers:
			b.WriteString(m.serversView.view(m))
		case tabEvents:
			b.WriteString(m.eventsView.view(m))
		case tabTools:
			b.WriteString(m.toolsView.view(m))
		}
	}
	b.WriteString("\n")
	b.WriteString(m.renderFooter())
	return b.String()
}

func (m model) renderHeader() string {
	left := headerStyle.Render("mcp-gateway")
	info := fmt.Sprintf("servers: %d  tools: %d", m.status.NumServers, m.status.NumTools)
	if !m.connected {
		info = errorStyle.Render("daemon disconnected")
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", info)
}

func (m model) renderTabBar() string {
	var parts []string
	for i := tab(0); i < numTabs; i++ {
		label := fmt.Sprintf("[%d]%s", i+1, i)
		if i == m.activeTab {
			parts = append(parts, tabActive.Render(label))
		} else {
			parts = append(parts, tabInactive.Render(label))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) renderFooter() string {
	var hints string
	if m.detail != nil {
		hints = "esc:back  r:restart  t:toggle"
	} else {
		switch m.activeTab {
		case tabServers:
			hints = "enter:detail  r:restart  t:toggle  1-3:tab  q:quit"
		case tabEvents:
			hints = "/:filter  1-3:tab  q:quit"
		case tabTools:
			hints = "/:filter  1-3:tab  q:quit"
		}
	}
	return helpStyle.Render(hints)
}
