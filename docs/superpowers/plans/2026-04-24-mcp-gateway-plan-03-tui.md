# mcp-gateway — Plan 03: TUI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Each phase has clear files + code blocks. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Ship v0.3 of `mcp-gateway` — a k9s-style TUI (`mcp-gateway tui`) for live observation and control of the running daemon. Read-only in v0.3 (no tool invocation). Three tabs: **Servers**, **Events**, **Tools**. Server-detail is a drill-down from Servers (inline, same screen).

**Architecture:** Bubble Tea (Charm) model-update-view. Root model holds the active tab + tab-specific submodels. An **AdminClient-backed poller** (2s tick) fires `tea.Msg`s with snapshots of `/admin/{status,servers,tools}`. An **SSE subscriber goroutine** pipes `/admin/events` into `tea.Msg`s for the Events tab. Keybindings route through the root model and dispatch to the active tab.

**Tech Stack:** `github.com/charmbracelet/bubbletea@latest`, `github.com/charmbracelet/lipgloss@latest`, `github.com/charmbracelet/bubbles@latest` (table, textinput, viewport, help). Existing `internal/adminclient` for HTTP-over-UNIX-socket. Existing `internal/admin` for the data shapes.

**Reference:** design spec `docs/superpowers/specs/2026-04-23-mcp-gateway-design.md` (§ 5), Plan 02 `docs/superpowers/plans/2026-04-24-mcp-gateway-plan-02-substrate.md` (admin RPC surface).

**v0.3 success criterion:** `mcp-gateway tui` opens, shows daemon status in header, lists servers with state / tool count / token cost in a live-updating table. Tab `2` shows streaming events. Tab `3` shows tools sorted by token cost. `enter` on a server drills into its detail view. `r`/`t` restart/toggle the highlighted server. `q`/`esc` quits. Survives daemon restarts gracefully (shows "disconnected" state, reconnects automatically).

**Not in this plan:** tool invocation (Plan 04), config editing in-TUI (Plan 04), secrets tab (the CLI `secret list` covers it), dashboard tab (folded into header).

---

## File Structure

```
mcp-gateway/
├── cmd/mcp-gateway/
│   └── tui.go                   # Cobra subcommand
├── internal/tui/
│   ├── tui.go                   # Program + root Model + Update + View + Init
│   ├── style.go                 # Lipgloss styles (shared)
│   ├── msgs.go                  # tea.Msg types + poller/subscriber
│   ├── servers.go               # Servers tab
│   ├── events.go                # Events tab
│   ├── tools.go                 # Tools tab
│   ├── detail.go                # Server detail drill-down
│   └── tui_test.go              # Update() tests (pure message-driven)
└── cmd/mcp-gateway/main.go      # +register newTUICmd()
```

---

## Phase 1 — Scaffold + admin polling + tab switching

### Task 1.1: Add Bubble Tea deps

```bash
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/lipgloss@latest
go get github.com/charmbracelet/bubbles@latest
go mod tidy
```

Commit: `chore: add bubbletea/lipgloss/bubbles deps`.

### Task 1.2: Cobra subcommand

Create `cmd/mcp-gateway/tui.go`:

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	tuipkg "github.com/ayu5h-raj/mcp-gateway/internal/tui"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
	"github.com/ayu5h-raj/mcp-gateway/internal/daemon"
)

func newTUICmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive TUI for live observation and control of the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			daemonHome := filepath.Join(home, ".mcp-gateway")
			sock := daemon.ChooseSocketPath(daemonHome)
			if _, err := os.Stat(sock); err != nil {
				return fmt.Errorf("daemon not running (no socket at %s)", sock)
			}
			c := adminclient.New(sock)
			return tuipkg.Run(c, sock)
		},
	}
}
```

Register in `cmd/mcp-gateway/main.go`'s `newRootCmd`:

```go
root.AddCommand(newTUICmd())
```

### Task 1.3: Scaffold internal/tui

Create `internal/tui/msgs.go`:

```go
package tui

import (
	"context"
	"encoding/json"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

// Poller messages — fired by a ticker into the root model's Update.
type statusMsg struct {
	Status admin.Status
	Err    error
}
type serversMsg struct {
	Servers []admin.ServerInfo
	Err     error
}
type toolsMsg struct {
	Tools []admin.ToolInfo
	Err   error
}

// Event stream messages.
type eventMsg struct{ Event event.Event }
type eventStreamDisconnectedMsg struct{ Err error }

// Action-result messages (acknowledging a user-initiated mutation).
type actionResultMsg struct {
	Op  string // "enable"|"disable"|"restart"
	Tgt string // server name
	Err error
}

// tick fires every pollInterval; the Update handler uses it to schedule the
// next refresh cycle.
type tickMsg time.Time

const pollInterval = 2 * time.Second

func tick() tea.Cmd {
	return tea.Tick(pollInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// cmdPoll returns a tea.Cmd that fetches status/servers/tools concurrently
// and emits three separate messages. Uses a short context so the UI stays
// responsive if the daemon stalls.
func cmdPoll(c *adminclient.Client) tea.Cmd {
	return tea.Batch(
		cmdFetchStatus(c),
		cmdFetchServers(c),
		cmdFetchTools(c),
	)
}

func cmdFetchStatus(c *adminclient.Client) tea.Cmd {
	return func() tea.Msg {
		var s admin.Status
		err := c.Get("/admin/status", &s)
		return statusMsg{Status: s, Err: err}
	}
}
func cmdFetchServers(c *adminclient.Client) tea.Cmd {
	return func() tea.Msg {
		var s []admin.ServerInfo
		err := c.Get("/admin/servers", &s)
		return serversMsg{Servers: s, Err: err}
	}
}
func cmdFetchTools(c *adminclient.Client) tea.Cmd {
	return func() tea.Msg {
		var t []admin.ToolInfo
		err := c.Get("/admin/tools", &t)
		return toolsMsg{Tools: t, Err: err}
	}
}

// cmdAction invokes an admin mutation and emits actionResultMsg.
func cmdAction(c *adminclient.Client, op, tgt string) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch op {
		case "enable":
			err = c.Post("/admin/servers/"+tgt+"/enable", nil, nil)
		case "disable":
			err = c.Post("/admin/servers/"+tgt+"/disable", nil, nil)
		case "restart":
			// No direct endpoint; disable+enable round-trip to force reconcile.
			if e := c.Post("/admin/servers/"+tgt+"/disable", nil, nil); e != nil {
				err = e
			} else {
				err = c.Post("/admin/servers/"+tgt+"/enable", nil, nil)
			}
		}
		return actionResultMsg{Op: op, Tgt: tgt, Err: err}
	}
}

// cmdSubscribeEvents opens the SSE stream and feeds eventMsg into the program.
// Reads in a goroutine until the program exits or the server disconnects; on
// disconnect, emits eventStreamDisconnectedMsg (the poller will pick up the
// daemon's return via subsequent tick retries).
func cmdSubscribeEvents(ctx context.Context, sock string, send func(tea.Msg)) {
	// Implemented in events.go. Stub here to keep msgs.go purely data.
	_ = ctx
	_ = sock
	_ = send
	_ = json.Marshal // placeholder import use; remove if unused after impl
}
```

Create `internal/tui/style.go`:

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent    = lipgloss.Color("39")  // cyan-ish
	colorMuted     = lipgloss.Color("240") // dim grey
	colorOK        = lipgloss.Color("42")  // green
	colorWarn      = lipgloss.Color("214") // orange
	colorError     = lipgloss.Color("203") // red
	colorDisabled  = lipgloss.Color("244") // dimmer grey

	headerStyle    = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	tabActive      = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Padding(0, 1)
	tabInactive    = lipgloss.NewStyle().Foreground(colorMuted).Padding(0, 1)
	helpStyle      = lipgloss.NewStyle().Foreground(colorMuted)
	errorStyle     = lipgloss.NewStyle().Foreground(colorError)
	disabledStyle  = lipgloss.NewStyle().Foreground(colorDisabled)

	stateGlyph = map[string]string{
		"running":    lipgloss.NewStyle().Foreground(colorOK).Render("●"),
		"starting":   lipgloss.NewStyle().Foreground(colorWarn).Render("◐"),
		"restarting": lipgloss.NewStyle().Foreground(colorWarn).Render("◑"),
		"errored":    lipgloss.NewStyle().Foreground(colorError).Render("!"),
		"disabled":   lipgloss.NewStyle().Foreground(colorDisabled).Render("○"),
		"stopped":    lipgloss.NewStyle().Foreground(colorMuted).Render("·"),
	}
)

// glyph returns the state glyph or "?" for unknown states.
func glyph(state string) string {
	if g, ok := stateGlyph[state]; ok {
		return g
	}
	return "?"
}
```

Create `internal/tui/tui.go`:

```go
// Package tui is a Bubble Tea TUI for mcp-gateway. k9s-style: three tabs
// (Servers, Events, Tools) driven by periodic /admin/{status,servers,tools}
// polling and an SSE subscription to /admin/events.
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

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

	w, h        int
	activeTab   tab
	connected   bool
	lastErr     error

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

// Add this import at the top of tui.go — put it in imports block:
// "github.com/charmbracelet/lipgloss"
```

**Implementer: add the `github.com/charmbracelet/lipgloss` import to tui.go; it's referenced by `lipgloss.JoinHorizontal`. The snippet above omits it from the import block for brevity.**

### Task 1.4: Sub-model stubs

Create `internal/tui/servers.go`, `events.go`, `tools.go`, `detail.go` with minimal stub models:

```go
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
```

```go
// events.go
package tui

import tea "github.com/charmbracelet/bubbletea"

type eventsModel struct{}

func newEventsModel() eventsModel { return eventsModel{} }

func (e eventsModel) Update(_ tea.Msg) (eventsModel, tea.Cmd) { return e, nil }
func (e eventsModel) view(_ model) string                     { return "  events view coming in phase 3" }
```

```go
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
```

```go
// detail.go
package tui

import tea "github.com/charmbracelet/bubbletea"

type detailModel struct {
	server string
}

func (d *detailModel) handleKey(m model, _ tea.KeyMsg) (tea.Model, tea.Cmd) { return m, nil }
func (d *detailModel) view(_ model) string                                   { return "  detail view coming in phase 5" }
```

### Task 1.5: Smoke test

```bash
make build
# In one terminal: mcp-gateway daemon
# In another:
./bin/mcp-gateway tui
```

Expected: TUI opens on Servers tab with "(no servers configured)" or a stub. Tabs switch with 1/2/3. Quits on q. Header updates every 2s showing `servers: N tools: N`. Daemon disconnect shows "daemon disconnected" in red.

Verify `go build ./... && go vet ./... && go test -race -count=1 ./...` clean.

Commit: `feat(tui): scaffold Bubble Tea model, tabs, admin polling`.

---

## Phase 2 — Servers tab (table + keybindings)

### Task 2.1: Rewrite `servers.go` with a real table

Replace the stub with:

```go
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
		s.selected = max(0, len(list)-1)
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

func (s serversModel) view(m model) string {
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
```

Add `lipgloss` import.

### Task 2.2: Verify

```bash
make build
# start daemon, add a server or two
mcp-gateway add echo --command sh --arg -c --arg 'cat'
./bin/mcp-gateway tui
```

Verify: j/k navigates, t toggles enable, r restarts, enter tries to drill in (shows phase-5 stub).

Commit: `feat(tui): servers tab with state glyphs, nav, toggle/restart actions`.

---

## Phase 3 — Events tab (SSE subscription)

### Task 3.1: SSE subscriber

Replace `cmdSubscribeEvents` stub in `msgs.go` with a real implementation, and wire it into `Run()` so the program lifecycle owns the goroutine.

Update `internal/tui/tui.go` Run():

```go
func Run(c *adminclient.Client, sock string) error {
	m := model{
		client:      c,
		sock:        sock,
		serversView: newServersModel(),
		eventsView:  newEventsModel(),
		toolsView:   newToolsModel(),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())

	// Start SSE subscriber. It sends messages into the program via p.Send.
	ctx, cancel := context.WithCancel(context.Background())
	go subscribeEvents(ctx, sock, p.Send)
	defer cancel()

	_, err := p.Run()
	return err
}
```

Create the subscriber in `internal/tui/events.go` (replacing the stub):

```go
package tui

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

// eventsModel holds the scrolling event list (newest at bottom).
type eventsModel struct {
	events  []event.Event
	filter  string // substring match; empty = no filter
	maxRing int
}

func newEventsModel() eventsModel { return eventsModel{maxRing: 500} }

// append adds an event, trimming the ring at maxRing.
func (e eventsModel) append(ev event.Event) eventsModel {
	e.events = append(e.events, ev)
	if over := len(e.events) - e.maxRing; over > 0 {
		e.events = e.events[over:]
	}
	return e
}

func (e eventsModel) Update(msg tea.Msg) (eventsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case eventMsg:
		e = e.append(msg.Event)
	case tea.KeyMsg:
		switch msg.String() {
		case "/":
			// Simple prompt: append mode; in v0.3 we use a single-character prompt model.
			// Full filter textinput deferred to Phase 6 polish.
		}
	}
	return e, nil
}

func (e eventsModel) view(m model) string {
	if len(e.events) == 0 {
		return disabledStyle.Render("\n  (no events yet — waiting on daemon activity)\n")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-8s %-26s %-14s %-30s %s",
		"TIME", "KIND", "SERVER", "METHOD", "DETAIL")))
	b.WriteString("\n")
	show := e.events
	// Show the last ~half-screen-worth (viewport size is simple for v0.3).
	if m.h > 0 && len(show) > m.h-6 {
		show = show[len(show)-(m.h-6):]
	}
	for _, ev := range show {
		if e.filter != "" && !matchesFilter(ev, e.filter) {
			continue
		}
		detail := ""
		if ev.Error != "" {
			detail = errorStyle.Render(ev.Error)
		} else if ev.Duration > 0 {
			detail = fmt.Sprintf("%v", ev.Duration.Round(time.Millisecond))
		}
		b.WriteString(fmt.Sprintf("  %-8s %-26s %-14s %-30s %s\n",
			ev.Time.Format("15:04:05"), truncate(ev.Kind, 26),
			truncate(ev.Server, 14), truncate(ev.Method, 30), detail))
	}
	return b.String()
}

func matchesFilter(ev event.Event, f string) bool {
	f = strings.ToLower(f)
	return strings.Contains(strings.ToLower(ev.Kind), f) ||
		strings.Contains(strings.ToLower(ev.Server), f) ||
		strings.Contains(strings.ToLower(ev.Method), f)
}

// subscribeEvents connects to /admin/events via the UNIX socket and pushes
// each SSE data frame into the program via send. Reconnects on drop with
// short backoff. Exits when ctx is cancelled.
func subscribeEvents(ctx context.Context, sock string, send func(tea.Msg)) {
	backoff := 500 * time.Millisecond
	for ctx.Err() == nil {
		err := streamEvents(ctx, sock, send)
		if err != nil && ctx.Err() == nil {
			send(eventStreamDisconnectedMsg{Err: err})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 5*time.Second {
			backoff *= 2
		}
	}
}

func streamEvents(ctx context.Context, sock string, send func(tea.Msg)) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	client := &http.Client{Transport: tr}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://x/admin/events", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return err
		}
		line = bytes.TrimRight(line, "\r\n")
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		var ev event.Event
		if jerr := json.Unmarshal(payload, &ev); jerr != nil {
			continue
		}
		send(eventMsg{Event: ev})
	}
}
```

### Task 3.2: Verify

Run the daemon with a real server attached, open the TUI, trigger some tool calls (from Claude Desktop or mgw-smoke), switch to the Events tab with `2`. You should see `mcp.request`/`mcp.response` events streaming in.

Commit: `feat(tui): events tab with live SSE subscription + auto-reconnect`.

---

## Phase 4 — Tools tab

### Task 4.1: Rewrite `tools.go`

```go
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
	// Sort by est_tokens desc, then by name.
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
```

Add `lipgloss` import.

Commit: `feat(tui): tools tab sorted by token cost with description preview`.

---

## Phase 5 — Server detail drill-down

### Task 5.1: Rewrite `detail.go`

```go
package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
)

// detailModel shows one server's full info + its tools.
type detailModel struct {
	server string // name
}

func (d *detailModel) handleKey(m model, k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "r":
		return m, cmdAction(m.client, "restart", d.server)
	case "t":
		// Need current enabled state to decide direction.
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
	b.WriteString(fmt.Sprintf("  started_at   : %s\n", s.StartedAt.Format("2006-01-02 15:04:05")))
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
```

Commit: `feat(tui): server detail drill-down with tools list`.

---

## Phase 6 — Polish

### Task 6.1: Help overlay

Add a `showHelp bool` to model. Handle `?` key to toggle. In `View()`, if `showHelp`, render a centered help box instead of the active tab.

### Task 6.2: Filter textinput on Events and Tools

Import `github.com/charmbracelet/bubbles/textinput`. Add a `filter textinput.Model` field to `eventsModel` and `toolsModel`. `/` → enable filter mode; input chars update filter; `esc` exits filter mode.

### Task 6.3: Graceful empty states + error banner

If `m.lastErr != nil`, show a dim red banner above the tab content: "⚠ {err.Error()} — retrying every 2s".

### Task 6.4: Commit

```
feat(tui): ? help overlay, / filter on events+tools, error banner
```

---

## Phase 7 — Tests + final pass + tag

### Task 7.1: Update-function tests

Create `internal/tui/tui_test.go` with pure-message tests. Bubble Tea models are pure on Update, so we can exercise them without a real TTY.

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

func TestModel_TabSwitching(t *testing.T) {
	m := model{
		serversView: newServersModel(),
		eventsView:  newEventsModel(),
		toolsView:   newToolsModel(),
	}
	for _, tc := range []struct {
		key  string
		want tab
	}{
		{"2", tabEvents},
		{"3", tabTools},
		{"1", tabServers},
	} {
		next, _ := m.Update(tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(tc.key)}))
		m = next.(model)
		assert.Equal(t, tc.want, m.activeTab, "after key %q", tc.key)
	}
}

func TestModel_StatusUpdatesConnection(t *testing.T) {
	m := model{}
	next, _ := m.Update(statusMsg{Status: admin.Status{PID: 42, NumServers: 3}})
	m = next.(model)
	assert.True(t, m.connected)
	assert.Equal(t, 42, m.status.PID)

	next, _ = m.Update(statusMsg{Err: assert.AnError})
	m = next.(model)
	assert.False(t, m.connected)
}

func TestModel_ServersMsgPopulatesView(t *testing.T) {
	m := model{serversView: newServersModel()}
	list := []admin.ServerInfo{
		{Name: "a", State: "running", ToolCount: 5, EstTokens: 100},
		{Name: "b", State: "disabled"},
	}
	next, _ := m.Update(serversMsg{Servers: list})
	m = next.(model)
	require.Len(t, m.servers, 2)
	assert.Equal(t, "a", m.serversView.servers[0].Name)
}

func TestEventsModel_AppendTrimsRing(t *testing.T) {
	e := eventsModel{maxRing: 3}
	for i := 0; i < 5; i++ {
		e = e.append(event.Event{Kind: "x"})
	}
	assert.Len(t, e.events, 3)
}

func TestToolsModel_SortsByTokensDesc(t *testing.T) {
	tm := newToolsModel()
	tm = tm.withTools([]admin.ToolInfo{
		{Name: "small", EstTokens: 10},
		{Name: "big", EstTokens: 1000},
		{Name: "medium", EstTokens: 500},
	})
	require.Len(t, tm.tools, 3)
	assert.Equal(t, "big", tm.tools[0].Name)
	assert.Equal(t, "medium", tm.tools[1].Name)
	assert.Equal(t, "small", tm.tools[2].Name)
}
```

### Task 7.2: Lint + full test + e2e

```bash
go vet ./...
go test -race -count=1 ./...
make e2e
# make lint  # if golangci-lint v2 is installed locally
```

All must be green.

### Task 7.3: Update README

Add a `## TUI` section after `## Day-to-day commands`:

```markdown
## TUI

`mcp-gateway tui` opens a k9s-style live view of the running daemon:

- **Servers tab** — all configured servers, their state, tool count, token cost. `j/k` to navigate, `enter` to drill in, `r` to restart, `t` to toggle, `esc` to go back.
- **Events tab** — streaming MCP request/response events + lifecycle events (child.attached, child.crashed, tools.changed) via live SSE from `/admin/events`.
- **Tools tab** — all tools across all servers, sorted by estimated token cost. Answers "what's eating my context?"

Press `?` for the full key map, `q` to quit.

Requires the daemon to be running. If it's not, the TUI reports `daemon disconnected` and auto-reconnects when you `mcp-gateway start`.
```

### Task 7.4: Tag + push

```bash
git tag v0.3.0-alpha
git push origin main
git push origin v0.3.0-alpha
gh run list --repo ayu5h-raj/mcp-gateway --limit 2
```

Wait for CI green. Commit: `docs(readme): TUI section` (before tagging).

---

## v0.3 acceptance checklist

- [ ] `mcp-gateway tui` opens when daemon is running.
- [ ] Header shows `servers: N  tools: N` and updates every 2s.
- [ ] Tabs 1/2/3 switch between Servers/Events/Tools.
- [ ] Servers tab: j/k navigate; r / t dispatch admin actions and refresh within 2s.
- [ ] Events tab: shows a live stream when the daemon is busy.
- [ ] Tools tab: sorted by tokens desc; description preview below selection.
- [ ] Server detail: `enter` drills in; `esc` back out; shows config + tool list.
- [ ] Daemon stopped externally: header goes red; TUI doesn't crash; auto-reconnects when daemon returns.
- [ ] `?` shows help; `q` quits.
- [ ] `go vet` + `go test -race ./...` + `make e2e` all pass.

---

## Known carry-overs to Plan 04+

- **Tool invocation** (call a tool from the Tools or Detail tab with a JSON-args prompt). Big scope, dedicated plan.
- **In-TUI config editing** (add/rm/enable/disable buttons with a form). Current CLI is fine for v0.3; this is convenience.
- **Keychain + secret-set from TUI.** Still deferred — hardcoded/env-ref secrets are our v0.3 story.
- **Fancier layouts** (split panes, floating windows). v0.3 uses plain top-to-bottom layout; Lipgloss can do more.
- **`mcp-gateway tui --once --json`** (dump current state as JSON, for scripting). Nice-to-have.
- **Scroll in tabs when content exceeds height.** v0.3 shows "last-N" windows; real viewport scrolling is a polish pass.
