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
		// Filter textinput deferred to polish phase; "/" is a no-op in v0.3.
		_ = msg
	}
	return e, nil
}

func (e eventsModel) view(m model) string {
	if len(e.events) == 0 {
		return "\n" + disabledText.Render("(no events yet — waiting on daemon activity)") + "\n"
	}
	const (
		timeW   = 8
		kindW   = 22
		serverW = 12
		methodW = 24
	)
	var b strings.Builder
	b.WriteString(" ")
	b.WriteString(colHeader.Render(fmt.Sprintf(
		" %-*s %-*s %-*s %-*s  %s",
		timeW, "TIME",
		kindW, "KIND",
		serverW, "SERVER",
		methodW, "METHOD",
		"DETAIL",
	)))
	b.WriteString("\n")

	show := e.events
	// Simple viewport: show the last N rows where N fits under the terminal.
	// Chrome matches Servers (7 rows).
	if m.h > 0 && len(show) > m.h-7 {
		show = show[len(show)-(m.h-7):]
	}
	for _, ev := range show {
		if e.filter != "" && !matchesFilter(ev, e.filter) {
			continue
		}
		detail := ""
		switch {
		case ev.Error != "":
			detail = errorText.Render(ev.Error)
		case ev.Duration > 0:
			detail = mutedText.Render(ev.Duration.Round(time.Millisecond).String())
		}
		fmt.Fprintf(&b, "  %-*s %-*s %-*s %-*s  %s\n",
			timeW, mutedText.Render(ev.Time.Format("15:04:05")),
			kindW, kindStyle(ev.Kind, kindW),
			serverW, truncate(ev.Server, serverW),
			methodW, truncate(ev.Method, methodW),
			detail,
		)
	}
	return b.String()
}

// kindStyle colors well-known event kinds. Returns a padded, styled string.
func kindStyle(kind string, width int) string {
	padded := padRight(truncate(kind, width), width)
	switch kind {
	case "mcp.request", "mcp.response":
		return accentText.Render(padded)
	case "child.attached":
		return stateStyle["running"].Render(padded)
	case "child.crashed", "child.disabled":
		return errorText.Render(padded)
	case "child.restarted":
		return stateStyle["restarting"].Render(padded)
	case "config.reload":
		return mutedText.Render(padded)
	default:
		return padded
	}
}

func matchesFilter(ev event.Event, f string) bool {
	f = strings.ToLower(f)
	return strings.Contains(strings.ToLower(ev.Kind), f) ||
		strings.Contains(strings.ToLower(ev.Server), f) ||
		strings.Contains(strings.ToLower(ev.Method), f)
}

// subscribeEvents connects to /admin/events via the UNIX socket and pushes
// each SSE data frame into the program via send. Reconnects on drop with
// exponential backoff. Exits when ctx is cancelled.
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
