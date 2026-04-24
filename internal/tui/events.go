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
		return disabledStyle.Render("\n  (no events yet — waiting on daemon activity)\n")
	}
	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("  %-8s %-26s %-14s %-30s %s",
		"TIME", "KIND", "SERVER", "METHOD", "DETAIL")))
	b.WriteString("\n")
	show := e.events
	// Show the last ~half-screen-worth (simple viewport for v0.3).
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
