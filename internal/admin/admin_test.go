package admin

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
)

type mockDaemon struct {
	mu       sync.Mutex
	status   Status
	servers  []ServerInfo
	tools    []ToolInfo
	bus      *event.Bus
	cfgBytes []byte
	cfgPath  string
	addCalls []ServerSpec
	removed  []string
	enabled  []string
	disabled []string
	reloads  int
}

func newMockDaemon() *mockDaemon {
	return &mockDaemon{bus: event.New(64)}
}

func (m *mockDaemon) Status() Status        { return m.status }
func (m *mockDaemon) Servers() []ServerInfo { return m.servers }
func (m *mockDaemon) Server(n string) (ServerInfo, bool) {
	for _, s := range m.servers {
		if s.Name == n {
			return s, true
		}
	}
	return ServerInfo{}, false
}
func (m *mockDaemon) Tools() []ToolInfo            { return m.tools }
func (m *mockDaemon) Bus() *event.Bus              { return m.bus }
func (m *mockDaemon) ConfigPath() string           { return m.cfgPath }
func (m *mockDaemon) ConfigBytes() ([]byte, error) { return m.cfgBytes, nil }

func (m *mockDaemon) AddServer(s ServerSpec) error {
	m.mu.Lock()
	m.addCalls = append(m.addCalls, s)
	m.mu.Unlock()
	return nil
}
func (m *mockDaemon) RemoveServer(n string) error {
	m.mu.Lock()
	m.removed = append(m.removed, n)
	m.mu.Unlock()
	return nil
}
func (m *mockDaemon) EnableServer(n string) error {
	m.mu.Lock()
	m.enabled = append(m.enabled, n)
	m.mu.Unlock()
	return nil
}
func (m *mockDaemon) DisableServer(n string) error {
	m.mu.Lock()
	m.disabled = append(m.disabled, n)
	m.mu.Unlock()
	return nil
}
func (m *mockDaemon) Reload() error {
	m.mu.Lock()
	m.reloads++
	m.mu.Unlock()
	return nil
}

func TestAdmin_GETStatus(t *testing.T) {
	d := newMockDaemon()
	d.status = Status{PID: 42, HTTPPort: 7823, Version: "0.2", NumServers: 1}
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var got Status
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, 42, got.PID)
	assert.Equal(t, 7823, got.HTTPPort)
}

func TestAdmin_GETServers(t *testing.T) {
	d := newMockDaemon()
	d.servers = []ServerInfo{
		{Name: "alpha", Prefix: "alpha", State: "running", ToolCount: 3, EstTokens: 100},
	}
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/servers")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []ServerInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "alpha", got[0].Name)
}

func TestAdmin_GETServerByName(t *testing.T) {
	d := newMockDaemon()
	d.servers = []ServerInfo{{Name: "alpha", State: "running"}}
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/servers/alpha")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp, err = http.Get(srv.URL + "/admin/servers/missing")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestAdmin_GETTools(t *testing.T) {
	d := newMockDaemon()
	d.tools = []ToolInfo{
		{Server: "alpha", Name: "alpha__hello", EstTokens: 25},
	}
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/tools")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []ToolInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.Len(t, got, 1)
	assert.Equal(t, "alpha__hello", got[0].Name)
}

func TestAdmin_GETSecretListsEnvRefsAndStatus(t *testing.T) {
	t.Setenv("GH_TOKEN", "set")
	os.Unsetenv("SLACK_BOT")

	d := newMockDaemon()
	d.cfgBytes = []byte(`{
		"version":1,
		"daemon":{"http_port":7823,"log_level":"info"},
		"mcpServers":{
			"github":{"command":"npx","env":{"GITHUB_TOKEN":"${env:GH_TOKEN}"},"enabled":true},
			"slack":{"command":"npx","env":{"SLACK_TOKEN":"${env:SLACK_BOT}","X":"${env:GH_TOKEN}"},"enabled":true}
		}
	}`)
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/secret")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []SecretInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	byName := map[string]SecretInfo{}
	for _, s := range got {
		byName[s.Name] = s
	}
	assert.ElementsMatch(t, []string{"github", "slack"}, byName["GH_TOKEN"].UsedBy)
	assert.True(t, byName["GH_TOKEN"].Set)
	assert.ElementsMatch(t, []string{"slack"}, byName["SLACK_BOT"].UsedBy)
	assert.False(t, byName["SLACK_BOT"].Set)
}

func TestAdmin_GETConfigReturnsBytes(t *testing.T) {
	d := newMockDaemon()
	d.cfgBytes = []byte(`{"version":1}`)
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/config")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	b := make([]byte, 64)
	n, _ := resp.Body.Read(b)
	assert.Contains(t, string(b[:n]), `"version":1`)
}

func TestAdmin_EventsSSEStreamsPublishedEvents(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/events")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	// Publish after subscribe.
	go func() {
		time.Sleep(50 * time.Millisecond)
		d.bus.Publish(event.Event{Kind: "test", Server: "alpha"})
	}()

	br := bufio.NewReader(resp.Body)
	deadline := time.Now().Add(2 * time.Second)
	var saw bool
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if len(line) > 6 && line[:6] == "data: " {
			saw = true
			break
		}
	}
	assert.True(t, saw, "should have read at least one SSE data: frame")
	_ = supervisor.StateRunning // silence import
}

func TestAdmin_POSTAddServer(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	body := `{"name":"github","command":"npx","args":["-y","@modelcontextprotocol/server-github"],"enabled":true}`
	resp, err := http.Post(srv.URL+"/admin/servers", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	require.Len(t, d.addCalls, 1)
	assert.Equal(t, "github", d.addCalls[0].Name)
}

func TestAdmin_DELETEServer(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/servers/github", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.Equal(t, []string{"github"}, d.removed)
}

func TestAdmin_POSTEnableDisable(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/admin/servers/github/enable", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"github"}, d.enabled)

	resp, err = http.Post(srv.URL+"/admin/servers/github/disable", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []string{"github"}, d.disabled)
}

func TestAdmin_POSTReload(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/admin/reload", "", nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 1, d.reloads)
}
