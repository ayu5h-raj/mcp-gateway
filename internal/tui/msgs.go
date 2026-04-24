package tui

import (
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

// subscribeEvents (defined in events.go) runs the SSE loop that feeds
// eventMsg / eventStreamDisconnectedMsg into the program.
