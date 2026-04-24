package tui

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

func keyMsg(s string) tea.KeyMsg {
	return tea.KeyMsg(tea.Key{Type: tea.KeyRunes, Runes: []rune(s)})
}

func newTestModel() model {
	return model{
		serversView: newServersModel(),
		eventsView:  newEventsModel(),
		toolsView:   newToolsModel(),
	}
}

func TestModel_TabSwitching(t *testing.T) {
	m := newTestModel()
	for _, tc := range []struct {
		key  string
		want tab
	}{
		{"2", tabEvents},
		{"3", tabTools},
		{"1", tabServers},
	} {
		next, _ := m.Update(keyMsg(tc.key))
		m = next.(model)
		assert.Equal(t, tc.want, m.activeTab, "after key %q", tc.key)
	}
}

func TestModel_StatusUpdatesConnection(t *testing.T) {
	m := newTestModel()
	next, _ := m.Update(statusMsg{Status: admin.Status{PID: 42, NumServers: 3}})
	m = next.(model)
	assert.True(t, m.connected)
	assert.Equal(t, 42, m.status.PID)

	next, _ = m.Update(statusMsg{Err: errors.New("boom")})
	m = next.(model)
	assert.False(t, m.connected)
	assert.NotNil(t, m.lastErr)
}

func TestModel_ServersMsgPopulatesView(t *testing.T) {
	m := newTestModel()
	list := []admin.ServerInfo{
		{Name: "a", State: "running", ToolCount: 5, EstTokens: 100},
		{Name: "b", State: "disabled"},
	}
	next, _ := m.Update(serversMsg{Servers: list})
	m = next.(model)
	require.Len(t, m.servers, 2)
	assert.Equal(t, "a", m.serversView.servers[0].Name)
}

func TestModel_HelpOverlayToggle(t *testing.T) {
	m := newTestModel()
	assert.False(t, m.showHelp)

	next, _ := m.Update(keyMsg("?"))
	m = next.(model)
	assert.True(t, m.showHelp)

	next, _ = m.Update(keyMsg("?"))
	m = next.(model)
	assert.False(t, m.showHelp)
}

func TestModel_EscClosesHelpBeforeDetail(t *testing.T) {
	m := newTestModel()
	m.showHelp = true
	m.detail = &detailModel{server: "x"}

	// Esc closes help first.
	next, _ := m.Update(keyMsg("esc"))
	m = next.(model)
	assert.False(t, m.showHelp)
	assert.NotNil(t, m.detail, "detail should still be open")

	// Second esc closes detail.
	next, _ = m.Update(keyMsg("esc"))
	m = next.(model)
	assert.Nil(t, m.detail)
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

func TestServersModel_EnterOpensDetail(t *testing.T) {
	m := newTestModel()
	m.serversView = m.serversView.withServers([]admin.ServerInfo{
		{Name: "alpha", State: "running"},
	})

	next, _ := m.Update(keyMsg("enter"))
	m = next.(model)
	require.NotNil(t, m.detail)
	assert.Equal(t, "alpha", m.detail.server)
}

func TestMatchesFilter(t *testing.T) {
	ev := event.Event{Kind: "mcp.request", Server: "kite", Method: "tools/list"}
	assert.True(t, matchesFilter(ev, "kite"))
	assert.True(t, matchesFilter(ev, "LIST"))
	assert.True(t, matchesFilter(ev, "request"))
	assert.False(t, matchesFilter(ev, "nope"))
}
