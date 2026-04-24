# mcp-gateway — Plan 02: Substrate + Secrets + Admin RPC + Mutation CLI

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship v0.2 of mcp-gateway adding (a) pidfile + lifecycle robustness, (b) in-process event bus, (c) token estimator, (d) `${secret:NAME}` resolver backed by macOS Keychain, (e) admin RPC over UNIX socket (read + write endpoints), (f) mutation CLI subcommands (`add`/`rm`/`enable`/`disable`/`list`/`secret`/`start`/`stop`/`restart`). After Plan 02, editing the JSONC by hand is optional, secrets never appear in plaintext, and a long-running daemon is robust against double-spawn.

**Architecture:** New packages under `internal/` are independent and stack: `pidfile` and `event` and `tokens` ship behind `internal/` first, then `secret` and `configwrite`, then `admin` (which depends on the prior packages plus a `Daemon` interface). `internal/daemon/daemon.go` is then modified to wire pidfile + event bus + secret resolver + a second UNIX-socket listener serving the admin mux. A small `internal/adminclient` (HTTP-over-UNIX-socket client) is consumed by new Cobra subcommands under `cmd/mcp-gateway/`.

**Tech Stack:** Go 1.25+, Cobra (CLI), `golang.org/x/sys/unix` (flock), `github.com/zalando/go-keyring` (macOS Keychain), `golang.org/x/term` (no-echo TTY input for `secret set`), stdlib `net/http`, `encoding/json`, `log/slog`, testify.

**Reference high-level plan:** `/Users/ayushraj/.claude/plans/federated-giggling-book.md`.
**Reference v0.1 plan (style template):** `docs/superpowers/plans/2026-04-23-mcp-gateway-plan-01-foundation.md`.

**v0.2 Success criterion:** A user can run `mcp-gateway start`, then `echo $TOKEN | mcp-gateway secret set github_token`, then `mcp-gateway add github --command npx --arg -y --arg @modelcontextprotocol/server-github --env GITHUB_TOKEN='${secret:github_token}'`, then see the github tools in `mcp-gateway list` — without ever editing JSONC by hand and without the token appearing in any log or config file. `grep -r '<the-real-token>' ~/.mcp-gateway/` returns zero matches.

**Not in this plan (deferred):**
- TUI (Plan 03)
- First-run wizard, launchd plist, goreleaser, brew tap (Plan 04)
- HTTP/SSE downstream MCPs, OAuth passthrough, sampling/elicitation, per-client scoping, Linux/Windows secret backends (Plan 05+)

---

## File Structure

```
mcp-gateway/
├── cmd/mcp-gateway/
│   ├── main.go                              # +new subcommand registrations
│   ├── add.go                               # new
│   ├── rm.go                                # new
│   ├── enable.go                            # new
│   ├── disable.go                           # new
│   ├── list.go                              # new
│   ├── secret.go                            # new
│   ├── start.go                             # new
│   ├── stop.go                              # new
│   └── restart.go                           # new
├── internal/
│   ├── pidfile/
│   │   ├── pidfile.go                       # new
│   │   └── pidfile_test.go                  # new
│   ├── event/
│   │   ├── event.go                         # new
│   │   ├── bus.go                           # new
│   │   └── bus_test.go                      # new
│   ├── tokens/
│   │   ├── tokens.go                        # new
│   │   └── tokens_test.go                   # new
│   ├── secret/
│   │   ├── secret.go                        # new (Resolver + parser + Backend interface)
│   │   ├── fake.go                          # new (in-memory backend for tests)
│   │   ├── keychain.go                      # new (macOS only, build tag darwin)
│   │   ├── secret_test.go                   # new
│   │   └── keychain_test.go                 # new (build tag keychain, skipped in CI)
│   ├── configwrite/
│   │   ├── configwrite.go                   # new
│   │   └── configwrite_test.go              # new
│   ├── admin/
│   │   ├── admin.go                         # new (Daemon interface + handlers)
│   │   ├── sse.go                           # new (SSE writer)
│   │   └── admin_test.go                    # new
│   ├── adminclient/
│   │   ├── adminclient.go                   # new
│   │   └── adminclient_test.go              # new
│   ├── daemon/
│   │   ├── daemon.go                        # +modify (wire pidfile, events, secrets, UNIX listener)
│   │   ├── http.go                          # +modify (NewMCPHandler accepts *event.Bus)
│   │   ├── http_test.go                     # +modify (update callers for new signature)
│   │   ├── admin_e2e_test.go                # new (build tag e2e)
│   │   └── (other files unchanged)
│   └── supervisor/
│       └── supervisor.go                    # +modify (add Hook field on SupervisorOpts)
└── README.md                                # +modify (CLI section)
```

---

## Phase 1 — Pidfile

Goal: a small package that takes an exclusive `flock` on a pidfile, writes the pid, and exposes a release function. Refuses to acquire if another process holds it.

### Task 1.1: pidfile — write the test first

**Files:**
- Create: `internal/pidfile/pidfile_test.go`

- [ ] **Step 1: Write the failing test**

```go
package pidfile

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAcquire_WritesPidAndReleases(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	release, existing, err := Acquire(path)
	require.NoError(t, err)
	require.Equal(t, 0, existing)
	require.NotNil(t, release)

	// pidfile contains our pid
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	got, err := strconv.Atoi(strings.TrimSpace(string(b)))
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), got)

	release()
	// File should be gone after release.
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err), "pidfile should be removed on release")
}

func TestAcquire_DoubleAcquireSameProcessFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	release1, _, err := Acquire(path)
	require.NoError(t, err)
	defer release1()

	// Same-process re-acquire should fail (flock is exclusive per fd; we use a fresh fd).
	_, existing, err := Acquire(path)
	require.Error(t, err)
	assert.Equal(t, os.Getpid(), existing)
}

func TestAcquire_AnotherProcessHoldsItFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	// Spawn a child that holds the pidfile for 2 seconds.
	holder := exec.Command("sh", "-c", "exec sleep 2")
	stdin, err := holder.StdinPipe()
	require.NoError(t, err)
	defer stdin.Close()

	// We can't directly call Acquire in another process without a binary, so
	// instead simulate by acquiring in this process and verifying a second
	// Acquire from a different file descriptor (same path, fresh open) fails.
	release1, _, err := Acquire(path)
	require.NoError(t, err)
	defer release1()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _, err := Acquire(path)
		if err != nil {
			return // expected
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("second Acquire should have failed while first holds the lock")
}

func TestAcquire_StalePidfileReclaimable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "daemon.pid")

	// Pre-write a stale pidfile (no flock → not held by anyone).
	require.NoError(t, os.WriteFile(path, []byte("99999\n"), 0o600))

	release, _, err := Acquire(path)
	require.NoError(t, err)
	defer release()
}
```

- [ ] **Step 2: Run — fails (Acquire undefined)**

```bash
go test ./internal/pidfile/ -v
```

### Task 1.2: pidfile — implement

**Files:**
- Create: `internal/pidfile/pidfile.go`

- [ ] **Step 1: Implement**

```go
// Package pidfile provides a flock-protected pidfile for ensuring a single
// running daemon instance.
package pidfile

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Acquire opens path, takes an exclusive non-blocking flock, writes the
// current pid, and returns a release function that unlocks and removes the
// pidfile. If another process holds the lock, returns an error and the pid
// of the holder (parsed from the file content; 0 if unparseable).
func Acquire(path string) (release func(), existingPid int, err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0, fmt.Errorf("pidfile: open %s: %w", path, err)
	}

	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		// Try to read existing pid for the error message.
		b, _ := os.ReadFile(path)
		pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, pid, fmt.Errorf("pidfile: another process (pid=%d) holds %s", pid, path)
		}
		return nil, pid, fmt.Errorf("pidfile: flock %s: %w", path, err)
	}

	// We hold the lock. Truncate and write our pid.
	if err := f.Truncate(0); err != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
		return nil, 0, fmt.Errorf("pidfile: truncate: %w", err)
	}
	if _, err := f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
		return nil, 0, fmt.Errorf("pidfile: write: %w", err)
	}

	release = func() {
		_ = os.Remove(path)
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}
	return release, 0, nil
}
```

- [ ] **Step 2: Add the dep**

```bash
go get golang.org/x/sys/unix
go mod tidy
```

- [ ] **Step 3: Run tests**

```bash
go test -race -count=1 ./internal/pidfile/ -v
```

Expected: 4/4 PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/pidfile/ go.mod go.sum
git commit -m "feat(pidfile): flock-protected pidfile package"
```

---

## Phase 2 — Event bus

Goal: in-process pub/sub with a ring buffer. Multiple subscribers, non-blocking publish (slow consumers drop), `Recent()` snapshot for late subscribers.

### Task 2.1: Event type

**Files:**
- Create: `internal/event/event.go`

- [ ] **Step 1: Implement**

```go
// Package event is an in-process pub/sub event bus with a ring buffer.
package event

import "time"

// Kind enumerates the well-known event kinds. New kinds may be added freely;
// consumers should treat unknown kinds as informational.
const (
	KindMCPRequest      = "mcp.request"
	KindMCPResponse     = "mcp.response"
	KindChildAttached   = "child.attached"
	KindChildCrashed    = "child.crashed"
	KindChildRestarted  = "child.restarted"
	KindChildDisabled   = "child.disabled"
	KindConfigReload    = "config.reload"
	KindToolsChanged    = "tools.changed"
	KindResourcesChanged = "resources.changed"
	KindPromptsChanged  = "prompts.changed"
)

// Event is a single bus message. All fields are optional except Time and Kind.
type Event struct {
	Time     time.Time      `json:"time"`
	Kind     string         `json:"kind"`
	Server   string         `json:"server,omitempty"`   // server prefix, if applicable
	Method   string         `json:"method,omitempty"`   // for mcp.* events
	Duration time.Duration  `json:"duration,omitempty"`
	Bytes    int            `json:"bytes,omitempty"`
	Error    string         `json:"error,omitempty"`
	Extra    map[string]any `json:"extra,omitempty"`
}
```

- [ ] **Step 2: Commit (no tests yet — the next task tests Bus which uses Event)**

```bash
git add internal/event/event.go
git commit -m "feat(event): Event type and well-known kinds"
```

### Task 2.2: Bus — write the test first

**Files:**
- Create: `internal/event/bus_test.go`

- [ ] **Step 1: Write the test**

```go
package event

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBus_PublishThenRecent(t *testing.T) {
	b := New(8)
	b.Publish(Event{Kind: "x", Server: "a"})
	b.Publish(Event{Kind: "x", Server: "b"})

	got := b.Recent()
	require.Len(t, got, 2)
	assert.Equal(t, "a", got[0].Server)
	assert.Equal(t, "b", got[1].Server)
}

func TestBus_RingOverwritesAtCapacity(t *testing.T) {
	b := New(3)
	for i := 0; i < 10; i++ {
		b.Publish(Event{Kind: "x", Method: string(rune('a' + i))})
	}
	got := b.Recent()
	require.Len(t, got, 3)
	// Last three: "h", "i", "j"
	assert.Equal(t, "h", got[0].Method)
	assert.Equal(t, "i", got[1].Method)
	assert.Equal(t, "j", got[2].Method)
}

func TestBus_SubscribeReceivesPublished(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	defer unsub()

	b.Publish(Event{Kind: "x", Server: "a"})

	select {
	case e := <-ch:
		assert.Equal(t, "a", e.Server)
	case <-time.After(time.Second):
		t.Fatal("subscriber did not receive published event")
	}
}

func TestBus_UnsubscribeStopsDelivery(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	unsub()

	b.Publish(Event{Kind: "x"})
	select {
	case e, ok := <-ch:
		if ok {
			t.Fatalf("subscriber received event after unsub: %v", e)
		}
	case <-time.After(100 * time.Millisecond):
		// Pass: no delivery.
	}
}

func TestBus_SlowSubscriberDropsInsteadOfBlocking(t *testing.T) {
	b := New(8)
	ch, unsub := b.Subscribe()
	defer unsub()

	// Don't drain ch. Publish 1000 events; bus must not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 1000; i++ {
			b.Publish(Event{Kind: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on slow subscriber")
	}

	// Some events were dropped; ch is bounded. Drain what's there to confirm
	// no panic.
	drained := 0
	for {
		select {
		case <-ch:
			drained++
		default:
			require.Greater(t, drained, 0)
			require.Less(t, drained, 1000) // dropped some
			return
		}
	}
}

func TestBus_ConcurrentPublishersAreSafe(t *testing.T) {
	b := New(1024)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Publish(Event{Kind: "x"})
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run — fails (New, Bus, Publish, Subscribe, Recent undefined)**

```bash
go test ./internal/event/ -v
```

### Task 2.3: Bus — implement

**Files:**
- Create: `internal/event/bus.go`

- [ ] **Step 1: Implement**

```go
package event

import (
	"sync"
	"time"
)

// subscriberBuffer is the per-subscriber channel size. Slower than the publish
// rate → events are dropped for that subscriber (Publish never blocks).
const subscriberBuffer = 64

// Bus is a fan-out event bus with a bounded ring buffer.
//
// Publish is non-blocking on subscribers (full subscriber channels drop the
// event for that subscriber only). Subscribe returns a channel and an
// unsubscribe function. Recent() returns a snapshot of the ring (oldest →
// newest) for late-attaching subscribers that want history.
type Bus struct {
	mu          sync.Mutex
	ring        []Event
	head        int
	full        bool
	capacity    int
	subscribers []chan Event
}

// New creates a Bus with the given ring capacity (default 1024 if 0 or
// negative).
func New(capacity int) *Bus {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Bus{
		ring:     make([]Event, capacity),
		capacity: capacity,
	}
}

// Publish appends an event to the ring and fans out to subscribers.
// Always non-blocking: full subscriber channels drop the event for that
// subscriber. Time is set if zero.
func (b *Bus) Publish(e Event) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	b.mu.Lock()
	b.ring[b.head] = e
	b.head = (b.head + 1) % b.capacity
	if b.head == 0 {
		b.full = true
	}
	subs := append([]chan Event(nil), b.subscribers...)
	b.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- e:
		default:
			// drop
		}
	}
}

// Subscribe returns a buffered channel that will receive future events, plus
// an unsubscribe function that removes the subscription and closes the channel.
// To replay history, call Recent() before Subscribe (so the snapshot precedes
// any drops).
func (b *Bus) Subscribe() (<-chan Event, func()) {
	ch := make(chan Event, subscriberBuffer)
	b.mu.Lock()
	b.subscribers = append(b.subscribers, ch)
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			out := b.subscribers[:0]
			for _, c := range b.subscribers {
				if c != ch {
					out = append(out, c)
				}
			}
			b.subscribers = out
			b.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsub
}

// Recent returns a snapshot of the ring buffer, oldest → newest.
func (b *Bus) Recent() []Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.full {
		out := make([]Event, b.head)
		copy(out, b.ring[:b.head])
		return out
	}
	out := make([]Event, b.capacity)
	copy(out, b.ring[b.head:])
	copy(out[b.capacity-b.head:], b.ring[:b.head])
	return out
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/event/ -v
```

Expected: 6/6 PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/event/bus.go internal/event/bus_test.go
git commit -m "feat(event): pub/sub bus with ring buffer and non-blocking publish"
```

---

## Phase 3 — Token estimator

Goal: a pluggable `Estimator` interface with a default `chars/4` implementation. `ToolTokens` sums name + description + raw schema bytes for an aggregator tool.

### Task 3.1: tokens — write the test first

**Files:**
- Create: `internal/tokens/tokens_test.go`

- [ ] **Step 1: Write the test**

```go
package tokens

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
)

func TestCharBy4_Tokens(t *testing.T) {
	e := CharBy4{}
	assert.Equal(t, 0, e.Tokens(""))
	assert.Equal(t, 1, e.Tokens("abcd"))
	assert.Equal(t, 2, e.Tokens("abcdefgh"))
	assert.Equal(t, 2, e.Tokens("abcdefghi")) // 9/4=2
}

func TestToolTokens_SumsNameDescriptionAndSchema(t *testing.T) {
	e := CharBy4{}
	tool := aggregator.Tool{
		Name:        "abcd",                        // 1
		Description: "abcdefgh",                    // 2
		InputSchema: []byte(`{"type":"object"}`),   // 17/4=4
	}
	got := ToolTokens(tool, e)
	assert.Equal(t, 1+2+4, got)
}
```

- [ ] **Step 2: Run — fails**

```bash
go test ./internal/tokens/ -v
```

### Task 3.2: tokens — implement

**Files:**
- Create: `internal/tokens/tokens.go`

- [ ] **Step 1: Implement**

```go
// Package tokens provides token-cost estimation for MCP tool definitions.
//
// v0.2 uses a chars/4 heuristic — clearly approximate, zero deps. The interface
// allows swapping in a real tokenizer later without touching consumers.
package tokens

import "github.com/ayu5h-raj/mcp-gateway/internal/aggregator"

// Estimator returns an approximate token count for a string.
type Estimator interface {
	Tokens(text string) int
}

// CharBy4 implements Estimator using the chars/4 heuristic. Good enough to
// answer "which server is eating my context budget?" — which is the only
// question the user actually asks.
type CharBy4 struct{}

// Tokens returns len(text)/4.
func (CharBy4) Tokens(text string) int { return len(text) / 4 }

// ToolTokens estimates the token cost of a tool definition: name + description
// + raw input schema bytes.
func ToolTokens(t aggregator.Tool, e Estimator) int {
	return e.Tokens(t.Name) + e.Tokens(t.Description) + e.Tokens(string(t.InputSchema))
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/tokens/ -v
```

Expected: 2/2 PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/tokens/
git commit -m "feat(tokens): chars/4 estimator + ToolTokens helper"
```

---

## Phase 4 — Secret resolver + fake backend

Goal: parse `${secret:NAME}` and `${env:NAME}` placeholders inside arbitrary strings; dispatch to a `Backend` per scheme. `${...}` lookups are hard errors; literal `$` is escaped as `$$`.

### Task 4.1: secret — write the test first

**Files:**
- Create: `internal/secret/secret_test.go`

- [ ] **Step 1: Write the test**

```go
package secret

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newResolver(t *testing.T) (*Resolver, *FakeBackend) {
	t.Helper()
	fb := NewFakeBackend()
	r := NewResolver()
	r.Register("secret", fb)
	r.Register("env", &EnvBackend{})
	return r, fb
}

func TestResolve_NoOpOnPlainString(t *testing.T) {
	r, _ := newResolver(t)
	out, err := r.Resolve("plain value")
	require.NoError(t, err)
	assert.Equal(t, "plain value", out)
}

func TestResolve_SecretSubstitution(t *testing.T) {
	r, fb := newResolver(t)
	require.NoError(t, fb.Set("github_token", "ghp_test"))

	out, err := r.Resolve("${secret:github_token}")
	require.NoError(t, err)
	assert.Equal(t, "ghp_test", out)
}

func TestResolve_EnvSubstitution(t *testing.T) {
	r, _ := newResolver(t)
	t.Setenv("MY_VAR", "hello")
	out, err := r.Resolve("prefix-${env:MY_VAR}-suffix")
	require.NoError(t, err)
	assert.Equal(t, "prefix-hello-suffix", out)
}

func TestResolve_MultipleSubstitutions(t *testing.T) {
	r, fb := newResolver(t)
	require.NoError(t, fb.Set("a", "ALPHA"))
	require.NoError(t, fb.Set("b", "BETA"))
	out, err := r.Resolve("${secret:a}/${secret:b}")
	require.NoError(t, err)
	assert.Equal(t, "ALPHA/BETA", out)
}

func TestResolve_MissingSecretErrors(t *testing.T) {
	r, _ := newResolver(t)
	_, err := r.Resolve("${secret:no_such_key}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_such_key")
}

func TestResolve_MissingEnvErrors(t *testing.T) {
	r, _ := newResolver(t)
	os.Unsetenv("DEFINITELY_NOT_SET_ENV_VAR_12345")
	_, err := r.Resolve("${env:DEFINITELY_NOT_SET_ENV_VAR_12345}")
	require.Error(t, err)
}

func TestResolve_UnknownSchemeErrors(t *testing.T) {
	r, _ := newResolver(t)
	_, err := r.Resolve("${nope:x}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nope")
}

func TestResolve_DollarDollarEscapeIsLiteralDollar(t *testing.T) {
	r, _ := newResolver(t)
	out, err := r.Resolve("price is $$5")
	require.NoError(t, err)
	assert.Equal(t, "price is $5", out)
}

func TestResolveEnv_AppliesToAllValues(t *testing.T) {
	r, fb := newResolver(t)
	require.NoError(t, fb.Set("token", "tok"))
	in := map[string]string{
		"GITHUB_TOKEN": "${secret:token}",
		"PLAIN":        "verbatim",
	}
	out, err := r.ResolveEnv(in)
	require.NoError(t, err)
	assert.Equal(t, "tok", out["GITHUB_TOKEN"])
	assert.Equal(t, "verbatim", out["PLAIN"])
}

func TestFakeBackend_CRUD(t *testing.T) {
	fb := NewFakeBackend()

	require.NoError(t, fb.Set("a", "1"))
	v, err := fb.Get("a")
	require.NoError(t, err)
	assert.Equal(t, "1", v)

	names, err := fb.List()
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, names)

	require.NoError(t, fb.Delete("a"))
	_, err = fb.Get("a")
	require.Error(t, err)
}
```

- [ ] **Step 2: Run — fails**

```bash
go test ./internal/secret/ -v
```

### Task 4.2: secret — implement Resolver, parser, EnvBackend

**Files:**
- Create: `internal/secret/secret.go`

- [ ] **Step 1: Implement**

```go
// Package secret resolves ${scheme:name} placeholders against pluggable
// backends. The default schemes are "secret" (keychain on macOS) and "env"
// (OS environment variables).
package secret

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// ErrNotFound is returned by a Backend when the requested name does not exist.
var ErrNotFound = errors.New("secret: not found")

// Backend stores secrets keyed by name.
type Backend interface {
	Get(name string) (string, error)
	Set(name, value string) error
	Delete(name string) error
	List() ([]string, error)
}

// Resolver dispatches ${scheme:name} placeholders to a registered Backend.
type Resolver struct {
	backends map[string]Backend
}

// NewResolver returns an empty Resolver. Register schemes via Register.
func NewResolver() *Resolver {
	return &Resolver{backends: map[string]Backend{}}
}

// Register adds a backend for a scheme name. The "env" and "secret" schemes
// are conventional but not auto-registered — daemon.Run wires them.
func (r *Resolver) Register(scheme string, b Backend) {
	r.backends[scheme] = b
}

// Resolve replaces every ${scheme:name} in s with the value from the
// corresponding backend. "$$" is an escape for a literal "$". Errors loudly
// on a missing backend, missing key, or malformed placeholder.
func (r *Resolver) Resolve(s string) (string, error) {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c != '$' {
			b.WriteByte(c)
			i++
			continue
		}
		// "$$" → literal "$"
		if i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i += 2
			continue
		}
		// Expect "${scheme:name}"
		if i+1 >= len(s) || s[i+1] != '{' {
			return "", fmt.Errorf("secret: stray $ at position %d", i)
		}
		end := strings.IndexByte(s[i+2:], '}')
		if end < 0 {
			return "", fmt.Errorf("secret: unterminated ${ at position %d", i)
		}
		body := s[i+2 : i+2+end]
		colon := strings.IndexByte(body, ':')
		if colon <= 0 {
			return "", fmt.Errorf("secret: invalid placeholder %q (need scheme:name)", body)
		}
		scheme, name := body[:colon], body[colon+1:]
		backend, ok := r.backends[scheme]
		if !ok {
			return "", fmt.Errorf("secret: unknown scheme %q in placeholder", scheme)
		}
		v, err := backend.Get(name)
		if err != nil {
			return "", fmt.Errorf("secret: %s:%s: %w", scheme, name, err)
		}
		b.WriteString(v)
		i += 2 + end + 1
	}
	return b.String(), nil
}

// ResolveEnv applies Resolve to every value in env, returning a new map.
// On any error, returns the partial result so far is discarded.
func (r *Resolver) ResolveEnv(env map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(env))
	for k, v := range env {
		resolved, err := r.Resolve(v)
		if err != nil {
			return nil, fmt.Errorf("env %s: %w", k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// EnvBackend looks up secrets in the OS environment.
type EnvBackend struct{}

// Get returns os.Getenv(name) or ErrNotFound if unset.
func (EnvBackend) Get(name string) (string, error) {
	v, ok := os.LookupEnv(name)
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set is unsupported for EnvBackend (env vars are caller-set).
func (EnvBackend) Set(string, string) error { return errors.New("env: read-only") }

// Delete is unsupported.
func (EnvBackend) Delete(string) error { return errors.New("env: read-only") }

// List returns nil (env enumeration is intentionally not exposed).
func (EnvBackend) List() ([]string, error) { return nil, nil }
```

### Task 4.3: secret — implement FakeBackend

**Files:**
- Create: `internal/secret/fake.go`

- [ ] **Step 1: Implement**

```go
package secret

import (
	"sort"
	"sync"
)

// FakeBackend is an in-memory Backend used in tests.
type FakeBackend struct {
	mu sync.Mutex
	m  map[string]string
}

// NewFakeBackend returns an empty FakeBackend.
func NewFakeBackend() *FakeBackend {
	return &FakeBackend{m: map[string]string{}}
}

// Get returns the value for name or ErrNotFound.
func (f *FakeBackend) Get(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.m[name]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set stores name → value.
func (f *FakeBackend) Set(name, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m[name] = value
	return nil
}

// Delete removes name.
func (f *FakeBackend) Delete(name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.m, name)
	return nil
}

// List returns sorted secret names.
func (f *FakeBackend) List() ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.m))
	for k := range f.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/secret/ -v
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/secret/secret.go internal/secret/fake.go internal/secret/secret_test.go
git commit -m "feat(secret): \${scheme:name} resolver with EnvBackend and FakeBackend"
```

---

## Phase 5 — Keychain backend

Goal: a `secret.Backend` implementation backed by macOS Keychain via `github.com/zalando/go-keyring`. Build-tagged `darwin` so Linux/Windows builds don't pull it. A separate test file is `keychain` build-tagged so it doesn't run in CI (which has no real keychain).

### Task 5.1: keychain — implement

**Files:**
- Create: `internal/secret/keychain.go`

- [ ] **Step 1: Add the dep**

```bash
go get github.com/zalando/go-keyring
go mod tidy
```

- [ ] **Step 2: Implement**

```go
//go:build darwin

// Package secret — keychain.go provides a Backend backed by macOS Keychain.
package secret

import (
	"errors"

	"github.com/zalando/go-keyring"
)

// KeychainService is the keychain "service" name under which all mcp-gateway
// secrets are stored. Each secret is `{Service: KeychainService, Account: name}`.
const KeychainService = "mcp-gateway"

// KeychainBackend stores secrets in the macOS Keychain.
type KeychainBackend struct{}

// NewKeychainBackend returns a KeychainBackend.
func NewKeychainBackend() *KeychainBackend { return &KeychainBackend{} }

// Get returns the value or ErrNotFound.
func (KeychainBackend) Get(name string) (string, error) {
	v, err := keyring.Get(KeychainService, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNotFound
		}
		return "", err
	}
	return v, nil
}

// Set stores the value in the keychain.
func (KeychainBackend) Set(name, value string) error {
	return keyring.Set(KeychainService, name, value)
}

// Delete removes the secret.
func (KeychainBackend) Delete(name string) error {
	if err := keyring.Delete(KeychainService, name); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// List is not supported by go-keyring directly; returns nil.
// The admin handler enumerates secret names from config.Server.Env values
// (via ${secret:NAME} extraction), not by enumerating the keychain itself.
func (KeychainBackend) List() ([]string, error) { return nil, nil }
```

### Task 5.2: keychain — manual integration test (skipped in CI)

**Files:**
- Create: `internal/secret/keychain_test.go`

- [ ] **Step 1: Write the test (build-tagged)**

```go
//go:build darwin && keychain

package secret

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeychain_RoundTrip(t *testing.T) {
	b := NewKeychainBackend()
	const key = "mcp-gateway-test-key-do-not-use"

	require.NoError(t, b.Set(key, "value-1"))
	t.Cleanup(func() { _ = b.Delete(key) })

	v, err := b.Get(key)
	require.NoError(t, err)
	assert.Equal(t, "value-1", v)

	require.NoError(t, b.Delete(key))
	_, err = b.Get(key)
	require.ErrorIs(t, err, ErrNotFound)
}
```

- [ ] **Step 2: Run manually (only when you have a real keychain available)**

```bash
go test -tags keychain -count=1 ./internal/secret/
```

Expected: PASS. CI does NOT pass `-tags keychain`, so the test is skipped there.

- [ ] **Step 3: Commit**

```bash
git add internal/secret/keychain.go internal/secret/keychain_test.go go.mod go.sum
git commit -m "feat(secret): macOS Keychain backend (build tag darwin)"
```

---

## Phase 6 — Config writer

Goal: atomic mutation of `config.jsonc`. Caller passes a `mutate` function that operates on a `*config.Config`; the writer parses the file, calls mutate, validates, then writes via tmp+rename. Comments are lost on round-trip (documented trade-off).

### Task 6.1: configwrite — write the test first

**Files:**
- Create: `internal/configwrite/configwrite_test.go`

- [ ] **Step 1: Write the test**

```go
package configwrite

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

const validConfig = `{
  "version": 1,
  "daemon": { "http_port": 7823, "log_level": "info" },
  "mcpServers": {
    "alpha": { "command": "echo", "enabled": true }
  }
}`

func writeTmp(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.jsonc")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestApply_AddsServer(t *testing.T) {
	path := writeTmp(t, validConfig)

	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	got, err := config.ParseFile(path)
	require.NoError(t, err)
	assert.Contains(t, got.MCPServers, "alpha")
	assert.Contains(t, got.MCPServers, "beta")
}

func TestApply_AtomicViaTempRename(t *testing.T) {
	path := writeTmp(t, validConfig)

	// After Apply, the file at path should still exist (no half-write).
	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	st, err := os.Stat(path)
	require.NoError(t, err)
	assert.False(t, st.IsDir())
}

func TestApply_ValidationFailureLeavesFileUntouched(t *testing.T) {
	path := writeTmp(t, validConfig)
	original, err := os.ReadFile(path)
	require.NoError(t, err)

	err = Apply(path, func(c *config.Config) error {
		// Set an invalid log level — Validate rejects.
		c.Daemon.LogLevel = "chatty"
		return nil
	})
	require.Error(t, err)

	// File on disk is unchanged.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after))
}

func TestApply_MutatorErrorBailsOut(t *testing.T) {
	path := writeTmp(t, validConfig)
	original, err := os.ReadFile(path)
	require.NoError(t, err)

	err = Apply(path, func(*config.Config) error {
		return assert.AnError
	})
	require.Error(t, err)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(original), string(after))
}

func TestApply_OutputIsValidJSON(t *testing.T) {
	path := writeTmp(t, validConfig)

	err := Apply(path, func(c *config.Config) error {
		c.MCPServers["beta"] = config.Server{Command: "cat", Enabled: true}
		return nil
	})
	require.NoError(t, err)

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw any
	require.NoError(t, json.Unmarshal(b, &raw))
}
```

- [ ] **Step 2: Run — fails**

```bash
go test ./internal/configwrite/ -v
```

### Task 6.2: configwrite — implement

**Files:**
- Create: `internal/configwrite/configwrite.go`

- [ ] **Step 1: Implement**

```go
// Package configwrite atomically mutates the on-disk config.jsonc.
//
// The flow is: read → parse → mutate → validate → write to tmp → atomic
// rename. JSONC comments are LOST on round-trip — the file is re-emitted as
// indented JSON. Documented trade-off vs. building a comment-preserving JSONC
// AST rewriter, which would be substantial engineering for cosmetic value.
package configwrite

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ayu5h-raj/mcp-gateway/internal/config"
)

// Apply parses cfgPath, runs mutate on the parsed Config, validates the
// result, then writes the file atomically (tmp + rename). On any failure
// the original file is untouched.
func Apply(cfgPath string, mutate func(*config.Config) error) error {
	cfg, err := config.ParseFile(cfgPath)
	if err != nil {
		return fmt.Errorf("configwrite: parse: %w", err)
	}
	if err := mutate(cfg); err != nil {
		return fmt.Errorf("configwrite: mutate: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("configwrite: validate: %w", err)
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("configwrite: marshal: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(cfgPath)
	tmp, err := os.CreateTemp(dir, ".config.jsonc.tmp.*")
	if err != nil {
		return fmt.Errorf("configwrite: tempfile: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath) // no-op if already renamed
	}()
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: write: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: chmod: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("configwrite: sync: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("configwrite: close: %w", err)
	}
	if err := os.Rename(tmpPath, cfgPath); err != nil {
		return fmt.Errorf("configwrite: rename: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/configwrite/ -v
```

Expected: 5/5 PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/configwrite/
git commit -m "feat(configwrite): atomic config mutation via tmp+rename"
```

---

## Phase 7 — Admin handler (read endpoints)

Goal: HTTP handler exposing GET endpoints over the (eventual) UNIX socket. Tested against a mock `Daemon` interface so admin works in isolation. SSE on `/admin/events` streams events from a `*event.Bus`.

### Task 7.1: admin — define the Daemon interface and stub handler

**Files:**
- Create: `internal/admin/admin.go`

- [ ] **Step 1: Implement (read endpoints only; write endpoints in Phase 8)**

```go
// Package admin serves the /admin/* HTTP surface used by the TUI (Plan 03)
// and mutation CLI subcommands. It is consumed only over the UNIX socket
// (file mode 0600) — never exposed on TCP. See daemon.Run.
package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ayu5h-raj/mcp-gateway/internal/aggregator"
	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/secret"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
	"github.com/ayu5h-raj/mcp-gateway/internal/tokens"
)

// Daemon is the surface admin handlers need. The real *daemon.Daemon
// implements it; tests use mocks.
type Daemon interface {
	Status() Status
	Servers() []ServerInfo
	Server(name string) (ServerInfo, bool)
	Tools() []ToolInfo
	Bus() *event.Bus
	SecretBackend() secret.Backend
	ConfigPath() string
	ConfigBytes() ([]byte, error)

	// Mutations — used by Phase 8.
	AddServer(spec ServerSpec) error
	RemoveServer(name string) error
	EnableServer(name string) error
	DisableServer(name string) error
	Reload() error
	SetSecret(name, value string) error
	DeleteSecret(name string) error
}

// Status is the daemon-level snapshot.
type Status struct {
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
	HTTPPort    int       `json:"http_port"`
	SocketPath  string    `json:"socket_path"`
	Version     string    `json:"version"`
	NumServers  int       `json:"num_servers"`
	NumTools    int       `json:"num_tools"`
	ConfigPath  string    `json:"config_path"`
}

// ServerInfo is the per-server view.
type ServerInfo struct {
	Name         string           `json:"name"`
	Prefix       string           `json:"prefix"`
	State        string           `json:"state"`
	Enabled      bool             `json:"enabled"`
	RestartCount int              `json:"restart_count"`
	StartedAt    time.Time        `json:"started_at,omitempty"`
	LastError    string           `json:"last_error,omitempty"`
	LogPath      string           `json:"log_path"`
	ToolCount    int              `json:"tool_count"`
	EstTokens    int              `json:"est_tokens"`
	Status       supervisor.State `json:"-"` // for internal use
}

// ToolInfo is the per-tool view.
type ToolInfo struct {
	Server      string `json:"server"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
	EstTokens   int    `json:"est_tokens"`
}

// ServerSpec is the body of POST /admin/server.
type ServerSpec struct {
	Name    string            `json:"name"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Prefix  string            `json:"prefix,omitempty"`
	Enabled bool              `json:"enabled"`
}

// SecretInfo is one entry in GET /admin/secret.
type SecretInfo struct {
	Name   string   `json:"name"`
	UsedBy []string `json:"used_by"`
}

// NewHandler returns the admin /admin/* http.Handler.
func NewHandler(d Daemon) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, d.Status())
	})
	mux.HandleFunc("/admin/servers", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, d.Servers())
		case http.MethodPost:
			handleAddServer(d, w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/admin/servers/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin/servers/")
		// /admin/servers/{name}, /admin/servers/{name}/enable, /admin/servers/{name}/disable
		switch {
		case strings.HasSuffix(path, "/enable") && r.Method == http.MethodPost:
			handleEnableServer(d, w, strings.TrimSuffix(path, "/enable"))
		case strings.HasSuffix(path, "/disable") && r.Method == http.MethodPost:
			handleDisableServer(d, w, strings.TrimSuffix(path, "/disable"))
		case r.Method == http.MethodGet:
			si, ok := d.Server(path)
			if !ok {
				http.NotFound(w, r)
				return
			}
			writeJSON(w, si)
		case r.Method == http.MethodDelete:
			handleRemoveServer(d, w, path)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/admin/tools", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, d.Tools())
	})
	mux.HandleFunc("/admin/events", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		serveSSE(d.Bus(), w, r)
	})
	mux.HandleFunc("/admin/secret", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		handleListSecrets(d, w, r)
	})
	mux.HandleFunc("/admin/secret/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/admin/secret/")
		switch r.Method {
		case http.MethodPost:
			handleSetSecret(d, w, r, name)
		case http.MethodDelete:
			handleDeleteSecret(d, w, name)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/admin/config", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		b, err := d.ConfigBytes()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/admin/reload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := d.Reload(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// --- helpers ---

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func handleAddServer(d Daemon, w http.ResponseWriter, r *http.Request) {
	var spec ServerSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := d.AddServer(spec); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func handleRemoveServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.RemoveServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleEnableServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.EnableServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleDisableServer(d Daemon, w http.ResponseWriter, name string) {
	if err := d.DisableServer(name); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleListSecrets(d Daemon, w http.ResponseWriter, _ *http.Request) {
	// Walk the config to find ${secret:NAME} references.
	cfgBytes, err := d.ConfigBytes()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	names := extractSecretNames(cfgBytes)
	out := make([]SecretInfo, 0, len(names))
	for name, used := range names {
		out = append(out, SecretInfo{Name: name, UsedBy: used})
	}
	writeJSON(w, out)
}

func handleSetSecret(d Daemon, w http.ResponseWriter, r *http.Request, name string) {
	var body struct {
		Value string `json:"value"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}
	if body.Value == "" {
		http.Error(w, "value required", http.StatusBadRequest)
		return
	}
	if err := d.SetSecret(name, body.Value); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteSecret(d Daemon, w http.ResponseWriter, name string) {
	if err := d.DeleteSecret(name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// extractSecretNames returns a map of secret name → server names that
// reference it. Pure: parses the JSON, walks mcpServers.*.env values, and
// finds ${secret:NAME} placeholders.
func extractSecretNames(cfgBytes []byte) map[string][]string {
	out := map[string][]string{}
	var raw struct {
		MCPServers map[string]struct {
			Env map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(cfgBytes, &raw); err != nil {
		return out
	}
	for srv, s := range raw.MCPServers {
		for _, v := range s.Env {
			for _, name := range scanSecretRefs(v) {
				out[name] = appendUnique(out[name], srv)
			}
		}
	}
	return out
}

func scanSecretRefs(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], "${secret:")
		if j < 0 {
			break
		}
		start := i + j + len("${secret:")
		end := strings.IndexByte(s[start:], '}')
		if end < 0 {
			break
		}
		out = append(out, s[start:start+end])
		i = start + end + 1
	}
	return out
}

func appendUnique(s []string, v string) []string {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

// stateString turns supervisor.State → string for JSON output.
func stateString(st supervisor.State) string { return st.String() }

// HelperToolsFromAggregator builds []ToolInfo from an aggregator snapshot
// using the chars/4 estimator. Exposed for the daemon.
func HelperToolsFromAggregator(snapshot []aggregator.Tool) []ToolInfo {
	est := tokens.CharBy4{}
	out := make([]ToolInfo, 0, len(snapshot))
	for _, t := range snapshot {
		out = append(out, ToolInfo{
			Server:      t.Server,
			Name:        t.Name,
			Description: t.Description,
			InputSchema: json.RawMessage(t.InputSchema),
			EstTokens:   tokens.ToolTokens(t, est),
		})
	}
	return out
}
```

### Task 7.2: admin — SSE writer

**Files:**
- Create: `internal/admin/sse.go`

- [ ] **Step 1: Implement**

```go
package admin

import (
	"encoding/json"
	"net/http"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
)

// serveSSE streams events from bus to the HTTP client until the client
// disconnects. On connect, the recent ring buffer is replayed first so a
// fresh client sees recent history.
func serveSSE(bus *event.Bus, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Replay recent.
	for _, e := range bus.Recent() {
		writeEvent(w, e)
	}
	flusher.Flush()

	// Subscribe.
	ch, unsub := bus.Subscribe()
	defer unsub()
	notify := r.Context().Done()
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return
			}
			writeEvent(w, e)
			flusher.Flush()
		case <-notify:
			return
		}
	}
}

func writeEvent(w http.ResponseWriter, e event.Event) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = w.Write([]byte("data: "))
	_, _ = w.Write(b)
	_, _ = w.Write([]byte("\n\n"))
}
```

### Task 7.3: admin — write the read-endpoint tests

**Files:**
- Create: `internal/admin/admin_test.go`

- [ ] **Step 1: Write tests with a mock Daemon**

```go
package admin

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ayu5h-raj/mcp-gateway/internal/event"
	"github.com/ayu5h-raj/mcp-gateway/internal/secret"
	"github.com/ayu5h-raj/mcp-gateway/internal/supervisor"
)

type mockDaemon struct {
	mu        sync.Mutex
	status    Status
	servers   []ServerInfo
	tools     []ToolInfo
	bus       *event.Bus
	sec       secret.Backend
	cfgBytes  []byte
	cfgPath   string
	addCalls  []ServerSpec
	removed   []string
	enabled   []string
	disabled  []string
	reloads   int
	setSec    map[string]string
	delSec    []string
}

func newMockDaemon() *mockDaemon {
	return &mockDaemon{
		bus:    event.New(64),
		sec:    secret.NewFakeBackend(),
		setSec: map[string]string{},
	}
}

func (m *mockDaemon) Status() Status                 { return m.status }
func (m *mockDaemon) Servers() []ServerInfo          { return m.servers }
func (m *mockDaemon) Server(n string) (ServerInfo, bool) {
	for _, s := range m.servers {
		if s.Name == n {
			return s, true
		}
	}
	return ServerInfo{}, false
}
func (m *mockDaemon) Tools() []ToolInfo              { return m.tools }
func (m *mockDaemon) Bus() *event.Bus                { return m.bus }
func (m *mockDaemon) SecretBackend() secret.Backend  { return m.sec }
func (m *mockDaemon) ConfigPath() string             { return m.cfgPath }
func (m *mockDaemon) ConfigBytes() ([]byte, error)   { return m.cfgBytes, nil }

func (m *mockDaemon) AddServer(s ServerSpec) error    { m.mu.Lock(); m.addCalls = append(m.addCalls, s); m.mu.Unlock(); return nil }
func (m *mockDaemon) RemoveServer(n string) error     { m.mu.Lock(); m.removed = append(m.removed, n); m.mu.Unlock(); return nil }
func (m *mockDaemon) EnableServer(n string) error     { m.mu.Lock(); m.enabled = append(m.enabled, n); m.mu.Unlock(); return nil }
func (m *mockDaemon) DisableServer(n string) error    { m.mu.Lock(); m.disabled = append(m.disabled, n); m.mu.Unlock(); return nil }
func (m *mockDaemon) Reload() error                   { m.mu.Lock(); m.reloads++; m.mu.Unlock(); return nil }
func (m *mockDaemon) SetSecret(n, v string) error     { m.mu.Lock(); m.setSec[n] = v; m.mu.Unlock(); return m.sec.Set(n, v) }
func (m *mockDaemon) DeleteSecret(n string) error     { m.mu.Lock(); m.delSec = append(m.delSec, n); m.mu.Unlock(); return m.sec.Delete(n) }

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

func TestAdmin_GETSecretListsNamesOnly(t *testing.T) {
	d := newMockDaemon()
	d.cfgBytes = []byte(`{
		"version":1,
		"daemon":{"http_port":7823,"log_level":"info"},
		"mcpServers":{
			"github":{"command":"npx","env":{"GITHUB_TOKEN":"${secret:gh_token}"},"enabled":true},
			"slack":{"command":"npx","env":{"SLACK_TOKEN":"${secret:slack_bot}","X":"${secret:gh_token}"},"enabled":true}
		}
	}`)
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/secret")
	require.NoError(t, err)
	defer resp.Body.Close()

	var got []SecretInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	names := map[string][]string{}
	for _, s := range got {
		names[s.Name] = s.UsedBy
	}
	assert.ElementsMatch(t, []string{"github", "slack"}, names["gh_token"])
	assert.ElementsMatch(t, []string{"slack"}, names["slack_bot"])
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
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/admin/ -v
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/admin/
git commit -m "feat(admin): /admin/* read endpoints + SSE for /admin/events"
```

---

## Phase 8 — Admin handler (write endpoints)

The write endpoints are already defined in Phase 7's handler (POST/DELETE on `/admin/servers`, `/admin/secret`, `/admin/reload`). This phase adds tests that verify the mock Daemon receives the right calls.

### Task 8.1: admin — write-endpoint tests

**Files:**
- Modify: `internal/admin/admin_test.go` (append)

- [ ] **Step 1: Append tests**

```go
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

func TestAdmin_POSTSecretWritesToBackend(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	body := `{"value":"ghp_test"}`
	resp, err := http.Post(srv.URL+"/admin/secret/github_token", "application/json", bytes.NewReader([]byte(body)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	v, err := d.sec.Get("github_token")
	require.NoError(t, err)
	assert.Equal(t, "ghp_test", v)
}

func TestAdmin_DELETESecretRemovesFromBackend(t *testing.T) {
	d := newMockDaemon()
	require.NoError(t, d.sec.Set("x", "y"))
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/secret/x", nil)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	_, err = d.sec.Get("x")
	require.Error(t, err)
}

func TestAdmin_POSTSecretRejectsEmptyValue(t *testing.T) {
	d := newMockDaemon()
	srv := httptest.NewServer(NewHandler(d))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/admin/secret/empty", "application/json", bytes.NewReader([]byte(`{"value":""}`)))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}
```

- [ ] **Step 2: Add `bytes` import to admin_test.go (if not present)**

- [ ] **Step 3: Run**

```bash
go test -race -count=1 ./internal/admin/ -v
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/admin_test.go
git commit -m "test(admin): write endpoints (add/rm/enable/disable/reload/secret)"
```

---

## Phase 9 — Daemon wiring

Goal: thread pidfile + event bus + secret resolver + UNIX-socket admin listener through `internal/daemon/daemon.go`. Update `internal/daemon/http.go` to accept `*event.Bus` and emit `mcp.request`/`mcp.response` events. Add a `Hook` field to `supervisor.SupervisorOpts` so the daemon can publish state-transition events.

### Task 9.1: supervisor — add Hook field

**Files:**
- Modify: `internal/supervisor/supervisor.go`

- [ ] **Step 1: Add `Hook` field**

In `SupervisorOpts`:

```go
type SupervisorOpts struct {
	LogDir             string
	MaxRestartAttempts int
	BackoffMaxSeconds  int
	// Hook, if non-nil, is called from the supervisor's manager goroutines
	// whenever a server transitions state. prev and next are the state
	// before/after the transition; err is non-nil for crash transitions.
	Hook func(name string, prev, next State, err error)
}
```

In every place where state is mutated under `s.mu`, capture old/new and call Hook AFTER releasing the lock. Specifically:

In `startServer`'s success branch (after setting StateRunning):

```go
prev := /* read before assigning */
srv.state = StateRunning
s.mu.Unlock()
if s.opts.Hook != nil {
    s.opts.Hook(name, prev, StateRunning, nil)
}
```

In the exit-watcher goroutine, similarly call Hook after each state transition (Errored, Disabled, Stopped).

For brevity here: after this Task, every transition emits a Hook callback. Write a small unit test (`TestSupervisor_HookFiresOnTransitions`) that registers a Hook, runs `Set`, waits for `StateRunning`, calls `Remove`, waits for `StateStopped`, and asserts the Hook saw at least Starting→Running and Running→Stopped.

- [ ] **Step 2: Run existing tests + new test**

```bash
go test -race -count=1 ./internal/supervisor/ -v
```

Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/supervisor/
git commit -m "feat(supervisor): add Hook callback on state transitions"
```

### Task 9.2: http.go — accept event bus and publish

**Files:**
- Modify: `internal/daemon/http.go`
- Modify: `internal/daemon/http_test.go`

- [ ] **Step 1: Change signature**

```go
func NewMCPHandler(agg *aggregator.Aggregator, bus *event.Bus) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req rpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			writeErr(w, nil, -32700, "parse error: "+err.Error())
			return
		}
		if isNotification(req) {
			w.WriteHeader(http.StatusAccepted)
			if bus != nil {
				bus.Publish(event.Event{Kind: event.KindMCPRequest, Method: req.Method})
			}
			return
		}
		start := time.Now()
		if bus != nil {
			bus.Publish(event.Event{Kind: event.KindMCPRequest, Method: req.Method})
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		resp := dispatch(ctx, agg, req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		if bus != nil {
			ev := event.Event{
				Kind: event.KindMCPResponse, Method: req.Method,
				Duration: time.Since(start),
			}
			if resp.Error != nil {
				ev.Error = resp.Error.Message
			}
			bus.Publish(ev)
		}
	})
}
```

Add `event` import.

- [ ] **Step 2: Update test callers**

In `http_test.go`'s tests: replace `NewMCPHandler(agg)` with `NewMCPHandler(agg, event.New(64))`.

- [ ] **Step 3: Run**

```bash
go test -race -count=1 ./internal/daemon/ -v
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/daemon/http.go internal/daemon/http_test.go
git commit -m "feat(daemon): publish mcp.request/mcp.response events on /mcp"
```

### Task 9.3: daemon.go — wire pidfile + secrets + admin mux + UNIX listener

**Files:**
- Modify: `internal/daemon/daemon.go`

- [ ] **Step 1: Update `Daemon` struct**

```go
type Daemon struct {
	Home     string
	Logger   *slog.Logger
	Version  string

	mu              sync.Mutex
	cfg             *config.Config
	sup             *supervisor.Supervisor
	agg             *aggregator.Aggregator
	clients         map[string]*mcpchild.Client
	events          *event.Bus
	secrets         *secret.Resolver
	keychain        secret.Backend
	pidfileRelease  func()
	socketPath      string
	startedAt       time.Time
	httpPort        int
}
```

- [ ] **Step 2: Update `New` constructor**

```go
func New(home string, logger *slog.Logger) *Daemon {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	keychain := secret.NewKeychainBackend()
	resolver := secret.NewResolver()
	resolver.Register("secret", keychain)
	resolver.Register("env", secret.EnvBackend{})
	return &Daemon{
		Home:     home,
		Logger:   logger,
		Version:  "0.2",
		clients:  map[string]*mcpchild.Client{},
		events:   event.New(10000),
		secrets:  resolver,
		keychain: keychain,
	}
}
```

Note: `secret.NewKeychainBackend()` is build-tagged `darwin`; for non-darwin builds add a `//go:build !darwin` stub in `internal/secret/keychain_other.go`:

```go
//go:build !darwin

package secret

// NewKeychainBackend on non-darwin platforms returns a backend that always
// errors. Plan 02 ships macOS-only; cross-platform later.
func NewKeychainBackend() Backend {
	return errBackend{err: errKeychainUnsupported}
}

type errBackend struct{ err error }

func (e errBackend) Get(string) (string, error)    { return "", e.err }
func (e errBackend) Set(string, string) error      { return e.err }
func (e errBackend) Delete(string) error           { return e.err }
func (e errBackend) List() ([]string, error)       { return nil, e.err }

var errKeychainUnsupported = errors.New("secret: keychain backend not built (darwin only in v0.2)")
```

(Add `errors` import.)

- [ ] **Step 3: Rewrite `Run` to open both listeners + pidfile**

```go
func (d *Daemon) Run(ctx context.Context) error {
	configPath := filepath.Join(d.Home, "config.jsonc")
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("config not found at %s: %w", configPath, err)
	}

	// Pidfile first.
	pidPath := filepath.Join(d.Home, "daemon.pid")
	release, existing, err := pidfile.Acquire(pidPath)
	if err != nil {
		return fmt.Errorf("daemon already running (pid=%d): %w", existing, err)
	}
	d.pidfileRelease = release
	defer func() {
		if d.pidfileRelease != nil {
			d.pidfileRelease()
		}
	}()

	watcher, err := config.NewWatcher(configPath)
	if err != nil {
		return fmt.Errorf("config watcher: %w", err)
	}
	defer watcher.Close()

	var initial *config.Config
	select {
	case initial = <-watcher.Changes():
	case err := <-watcher.Errors():
		return fmt.Errorf("initial config: %w", err)
	case <-ctx.Done():
		return ctx.Err()
	}

	logDir := filepath.Join(d.Home, "servers")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return fmt.Errorf("log dir: %w", err)
	}

	d.startedAt = time.Now()
	d.httpPort = initial.Daemon.HTTPPort
	d.socketPath = filepath.Join(d.Home, "sock")
	// Remove any stale socket file from a previous unclean shutdown.
	_ = os.Remove(d.socketPath)

	d.agg = aggregator.New()
	d.sup = supervisor.New(supervisor.SupervisorOpts{
		LogDir:             logDir,
		MaxRestartAttempts: initial.Daemon.ChildRestartMaxAttempts,
		BackoffMaxSeconds:  initial.Daemon.ChildRestartBackoffMaxSeconds,
		Hook: func(name string, prev, next supervisor.State, hookErr error) {
			d.events.Publish(event.Event{
				Kind:   event.KindChildAttached, // overwritten below per next-state
				Server: name,
				Extra:  map[string]any{"prev": prev.String(), "next": next.String()},
			})
			// Map next state → kind.
			ev := event.Event{Server: name, Extra: map[string]any{"prev": prev.String(), "next": next.String()}}
			switch next {
			case supervisor.StateRunning:
				ev.Kind = event.KindChildAttached
			case supervisor.StateErrored, supervisor.StateRestarting:
				ev.Kind = event.KindChildCrashed
				if hookErr != nil {
					ev.Error = hookErr.Error()
				}
			case supervisor.StateDisabled:
				ev.Kind = event.KindChildDisabled
			default:
				return
			}
			d.events.Publish(ev)
		},
	})
	go d.sup.Run(ctx)
	go d.attachLoop(ctx)

	// Wire aggregator change callbacks → events.
	d.agg.OnToolsChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindToolsChanged})
	})
	d.agg.OnResourcesChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindResourcesChanged})
	})
	d.agg.OnPromptsChanged(func() {
		d.events.Publish(event.Event{Kind: event.KindPromptsChanged})
	})

	d.reconcile(ctx, initial)

	// TCP listener — only /mcp.
	mcpMux := http.NewServeMux()
	mcpMux.Handle("/mcp", NewMCPHandler(d.agg, d.events))
	tcpAddr := fmt.Sprintf("127.0.0.1:%d", initial.Daemon.HTTPPort)
	tcpLn, err := net.Listen("tcp", tcpAddr)
	if err != nil {
		return fmt.Errorf("listen tcp %s: %w", tcpAddr, err)
	}
	tcpSrv := &http.Server{Handler: mcpMux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening (tcp)", "addr", tcpAddr)
	go func() {
		if err := tcpSrv.Serve(tcpLn); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("tcp serve", "err", err)
		}
	}()

	// UNIX socket listener — both /mcp and /admin/*.
	unixMux := http.NewServeMux()
	unixMux.Handle("/mcp", NewMCPHandler(d.agg, d.events))
	unixMux.Handle("/admin/", admin.NewHandler(d))
	unixLn, err := net.Listen("unix", d.socketPath)
	if err != nil {
		return fmt.Errorf("listen unix %s: %w", d.socketPath, err)
	}
	if err := os.Chmod(d.socketPath, 0o600); err != nil {
		return fmt.Errorf("chmod sock: %w", err)
	}
	unixSrv := &http.Server{Handler: unixMux, ReadHeaderTimeout: 5 * time.Second}
	d.Logger.Info("mcp-gateway listening (unix)", "sock", d.socketPath)
	go func() {
		if err := unixSrv.Serve(unixLn); err != nil && err != http.ErrServerClosed {
			d.Logger.Error("unix serve", "err", err)
		}
	}()

	for {
		select {
		case cfg := <-watcher.Changes():
			d.Logger.Info("config changed, reconciling")
			d.events.Publish(event.Event{Kind: event.KindConfigReload})
			d.reconcile(ctx, cfg)
		case err := <-watcher.Errors():
			d.Logger.Error("config error", "err", err)
		case <-ctx.Done():
			shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = tcpSrv.Shutdown(shutCtx)
			_ = unixSrv.Shutdown(shutCtx)
			cancel()
			_ = os.Remove(d.socketPath)
			return nil
		}
	}
}
```

Add imports: `net`, `internal/admin`, `internal/event`, `internal/pidfile`, `internal/secret`.

- [ ] **Step 4: Update `reconcile` to resolve secrets**

In `reconcile`, before calling `d.sup.Set(...)`:

```go
resolvedEnv, err := d.secrets.ResolveEnv(s.Env)
if err != nil {
    d.Logger.Error("secret resolution failed", "server", name, "err", err)
    continue
}
d.sup.Set(name, supervisor.ServerSpec{
    Name:    name,
    Command: s.Command,
    Args:    s.Args,
    Env:     resolvedEnv,
})
```

(Same in `reattach` if it constructs env — verify.)

- [ ] **Step 5: Implement the admin.Daemon interface methods on *Daemon**

Add to `daemon.go`:

```go
func (d *Daemon) Status() admin.Status {
	d.mu.Lock()
	numServers := len(d.clients)
	d.mu.Unlock()
	return admin.Status{
		PID:        os.Getpid(),
		StartedAt:  d.startedAt,
		HTTPPort:   d.httpPort,
		SocketPath: d.socketPath,
		Version:    d.Version,
		NumServers: numServers,
		NumTools:   len(d.agg.Tools()),
		ConfigPath: filepath.Join(d.Home, "config.jsonc"),
	}
}

func (d *Daemon) Servers() []admin.ServerInfo {
	d.mu.Lock()
	cfg := d.cfg
	d.mu.Unlock()
	if cfg == nil {
		return nil
	}
	statuses := d.sup.List()
	statusByName := map[string]supervisor.Status{}
	for _, s := range statuses {
		statusByName[s.Name] = s
	}
	tools := d.agg.Tools()
	tokensByPrefix := map[string]int{}
	countByPrefix := map[string]int{}
	est := tokens.CharBy4{}
	for _, t := range tools {
		tokensByPrefix[t.Server] += tokens.ToolTokens(t, est)
		countByPrefix[t.Server]++
	}
	out := make([]admin.ServerInfo, 0, len(cfg.MCPServers))
	for name, s := range cfg.MCPServers {
		prefix := config.EffectivePrefix(name, s)
		st := statusByName[name]
		errStr := ""
		if st.LastError != nil {
			errStr = st.LastError.Error()
		}
		out = append(out, admin.ServerInfo{
			Name:         name,
			Prefix:       prefix,
			State:        st.State.String(),
			Enabled:      s.Enabled,
			RestartCount: st.RestartCount,
			StartedAt:    st.StartedAt,
			LastError:    errStr,
			LogPath:      filepath.Join(d.Home, "servers", name+".log"),
			ToolCount:    countByPrefix[prefix],
			EstTokens:    tokensByPrefix[prefix],
		})
	}
	return out
}

func (d *Daemon) Server(name string) (admin.ServerInfo, bool) {
	for _, s := range d.Servers() {
		if s.Name == name {
			return s, true
		}
	}
	return admin.ServerInfo{}, false
}

func (d *Daemon) Tools() []admin.ToolInfo {
	return admin.HelperToolsFromAggregator(d.agg.Tools())
}

func (d *Daemon) Bus() *event.Bus { return d.events }

func (d *Daemon) SecretBackend() secret.Backend { return d.keychain }

func (d *Daemon) ConfigPath() string { return filepath.Join(d.Home, "config.jsonc") }

func (d *Daemon) ConfigBytes() ([]byte, error) {
	return os.ReadFile(d.ConfigPath())
}

func (d *Daemon) AddServer(spec admin.ServerSpec) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		c.MCPServers[spec.Name] = config.Server{
			Command: spec.Command,
			Args:    spec.Args,
			Env:     spec.Env,
			Enabled: spec.Enabled,
			Prefix:  spec.Prefix,
		}
		return nil
	})
}

func (d *Daemon) RemoveServer(name string) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		if _, ok := c.MCPServers[name]; !ok {
			return fmt.Errorf("no such server %q", name)
		}
		delete(c.MCPServers, name)
		return nil
	})
}

func (d *Daemon) EnableServer(name string) error  { return d.toggle(name, true) }
func (d *Daemon) DisableServer(name string) error { return d.toggle(name, false) }

func (d *Daemon) toggle(name string, enabled bool) error {
	return configwrite.Apply(d.ConfigPath(), func(c *config.Config) error {
		s, ok := c.MCPServers[name]
		if !ok {
			return fmt.Errorf("no such server %q", name)
		}
		s.Enabled = enabled
		c.MCPServers[name] = s
		return nil
	})
}

func (d *Daemon) Reload() error {
	// fsnotify will pick up a touch.
	now := time.Now()
	return os.Chtimes(d.ConfigPath(), now, now)
}

func (d *Daemon) SetSecret(name, value string) error {
	return d.keychain.Set(name, value)
}

func (d *Daemon) DeleteSecret(name string) error {
	return d.keychain.Delete(name)
}
```

Add imports: `internal/admin`, `internal/configwrite`, `internal/tokens`.

- [ ] **Step 6: Run all tests + e2e**

```bash
go test -race -count=1 ./... 2>&1 | tail -10
make e2e
```

Expected: all green (the v0.1 e2e still passes; admin endpoints are tested via mocks in admin_test).

- [ ] **Step 7: Commit**

```bash
git add internal/daemon/ internal/secret/keychain_other.go
git commit -m "feat(daemon): wire pidfile, event bus, secrets, UNIX socket + admin mux"
```

---

## Phase 10 — adminclient

Goal: tiny HTTP client that dials a UNIX socket. Used by every CLI mutation subcommand.

### Task 10.1: adminclient — write the test first

**Files:**
- Create: `internal/adminclient/adminclient_test.go`

- [ ] **Step 1: Write the test**

```go
package adminclient

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func startUnixServer(t *testing.T, h http.Handler) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	require.NoError(t, os.Chmod(sock, 0o600))
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = ln.Close(); _ = os.Remove(sock) })
	return sock
}

func TestClient_GetJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": 7})
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	var got map[string]any
	require.NoError(t, c.Get("/admin/status", &got))
	assert.Equal(t, true, got["ok"])
	assert.EqualValues(t, 7, got["n"].(float64))
}

func TestClient_PostJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/secret/x", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	require.NoError(t, c.Post("/admin/secret/x", map[string]string{"value": "y"}, nil))
}

func TestClient_Delete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/servers/foo", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "DELETE", r.Method)
		w.WriteHeader(http.StatusNoContent)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	require.NoError(t, c.Delete("/admin/servers/foo"))
}

func TestClient_NonOKReturnsError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/x", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusBadRequest)
	})
	sock := startUnixServer(t, mux)
	c := New(sock)

	err := c.Get("/admin/x", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}
```

- [ ] **Step 2: Run — fails**

```bash
go test ./internal/adminclient/ -v
```

### Task 10.2: adminclient — implement

**Files:**
- Create: `internal/adminclient/adminclient.go`

- [ ] **Step 1: Implement**

```go
// Package adminclient is a tiny HTTP client that talks to the daemon over a
// UNIX socket (typically ~/.mcp-gateway/sock).
package adminclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Client makes HTTP requests over a UNIX socket.
type Client struct {
	http *http.Client
	sock string
}

// New returns a Client that dials the given UNIX socket path.
func New(sock string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}
	return &Client{
		http: &http.Client{Transport: tr, Timeout: 10 * time.Second},
		sock: sock,
	}
}

// Get GETs path and decodes JSON into into (if non-nil).
func (c *Client) Get(path string, into any) error {
	return c.do(http.MethodGet, path, nil, into)
}

// Post POSTs body (json-marshaled if non-nil) to path and decodes JSON into into.
func (c *Client) Post(path string, body, into any) error {
	return c.do(http.MethodPost, path, body, into)
}

// Delete sends a DELETE.
func (c *Client) Delete(path string) error {
	return c.do(http.MethodDelete, path, nil, nil)
}

func (c *Client) do(method, path string, body, into any) error {
	url := "http://unix" + path
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("adminclient: marshal: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		return fmt.Errorf("adminclient: new request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("adminclient: do (sock=%s): %w", c.sock, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("adminclient: %s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if into == nil {
		return nil
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("adminclient: decode: %w", err)
	}
	return nil
}
```

- [ ] **Step 2: Run tests**

```bash
go test -race -count=1 ./internal/adminclient/ -v
```

Expected: 4/4 PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/adminclient/
git commit -m "feat(adminclient): tiny HTTP client over UNIX socket"
```

---

## Phase 11 — CLI: list, add, rm, enable, disable

Goal: 5 new Cobra subcommands wrapping `adminclient` calls.

### Task 11.1: list

**Files:**
- Create: `cmd/mcp-gateway/list.go`

- [ ] **Step 1: Implement**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List configured MCP servers and their state",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			c := adminclient.New(sock)
			var got []admin.ServerInfo
			if err := c.Get("/admin/servers", &got); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSTATE\tPREFIX\tTOOLS\t~TOKENS\tLAST ERROR")
			for _, s := range got {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t~%d\t%s\n", s.Name, s.State, s.Prefix, s.ToolCount, s.EstTokens, truncate(s.LastError, 40))
			}
			return tw.Flush()
		},
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
```

### Task 11.2: add

**Files:**
- Create: `cmd/mcp-gateway/add.go`

- [ ] **Step 1: Implement**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newAddCmd() *cobra.Command {
	var (
		command string
		args    []string
		envs    []string
		prefix  string
		disabled bool
	)
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add an MCP server (writes config + reconciles)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, posArgs []string) error {
			if command == "" {
				return fmt.Errorf("--command is required")
			}
			env := map[string]string{}
			for _, e := range envs {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					return fmt.Errorf("invalid --env %q (must be KEY=VALUE)", e)
				}
				env[k] = v
			}
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			c := adminclient.New(sock)
			spec := admin.ServerSpec{
				Name:    posArgs[0],
				Command: command,
				Args:    args,
				Env:     env,
				Prefix:  prefix,
				Enabled: !disabled,
			}
			if err := c.Post("/admin/servers", spec, nil); err != nil {
				return err
			}
			fmt.Printf("added %s\n", posArgs[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&command, "command", "", "executable to run (required)")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "argument to pass (repeatable)")
	cmd.Flags().StringArrayVar(&envs, "env", nil, "KEY=VALUE env var (repeatable; use ${secret:NAME} for keychain refs)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "tool prefix (default: server name)")
	cmd.Flags().BoolVar(&disabled, "disabled", false, "add but don't start")
	return cmd
}
```

### Task 11.3: rm, enable, disable

**Files:**
- Create: `cmd/mcp-gateway/rm.go`, `enable.go`, `disable.go`

- [ ] **Step 1: rm.go**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Remove an MCP server from the gateway config",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Delete("/admin/servers/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("removed %s\n", args[0])
			return nil
		},
	}
}
```

- [ ] **Step 2: enable.go**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newEnableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "enable <name>",
		Short: "Enable a previously-disabled MCP server",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Post("/admin/servers/"+args[0]+"/enable", nil, nil); err != nil {
				return err
			}
			fmt.Printf("enabled %s\n", args[0])
			return nil
		},
	}
}
```

- [ ] **Step 3: disable.go**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newDisableCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disable <name>",
		Short: "Disable an MCP server (stays in config; child stopped)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Post("/admin/servers/"+args[0]+"/disable", nil, nil); err != nil {
				return err
			}
			fmt.Printf("disabled %s\n", args[0])
			return nil
		},
	}
}
```

### Task 11.4: register subcommands

**Files:**
- Modify: `cmd/mcp-gateway/main.go`

- [ ] **Step 1: Add to `newRootCmd`**

```go
root.AddCommand(newDaemonCmd())
root.AddCommand(newStdioCmd())
root.AddCommand(newStatusCmd())
root.AddCommand(newListCmd())
root.AddCommand(newAddCmd())
root.AddCommand(newRmCmd())
root.AddCommand(newEnableCmd())
root.AddCommand(newDisableCmd())
```

- [ ] **Step 2: Build**

```bash
make build
./bin/mcp-gateway --help
```

Expected: list/add/rm/enable/disable visible.

- [ ] **Step 3: Commit**

```bash
git add cmd/mcp-gateway/
git commit -m "feat(cli): add list/add/rm/enable/disable subcommands via admin RPC"
```

---

## Phase 12 — CLI: secret

Goal: `mcp-gateway secret set NAME` reads value via stdin (no echo on TTY); `mcp-gateway secret list` prints names; `mcp-gateway secret rm NAME` deletes from keychain.

### Task 12.1: secret subcommand

**Files:**
- Create: `cmd/mcp-gateway/secret.go`

- [ ] **Step 1: Add the term dep**

```bash
go get golang.org/x/term
go mod tidy
```

- [ ] **Step 2: Implement**

```go
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/ayu5h-raj/mcp-gateway/internal/admin"
	"github.com/ayu5h-raj/mcp-gateway/internal/adminclient"
)

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Manage secrets stored in macOS Keychain",
	}
	cmd.AddCommand(newSecretSetCmd(), newSecretListCmd(), newSecretRmCmd())
	return cmd
}

func newSecretSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "set <name>",
		Short: "Set a secret. Value read from stdin (no echo on TTY).",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			name := args[0]
			value, err := readSecretValue()
			if err != nil {
				return err
			}
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Post("/admin/secret/"+name, map[string]string{"value": value}, nil); err != nil {
				return err
			}
			fmt.Printf("secret %s set\n", name)
			return nil
		},
	}
}

func readSecretValue() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprint(os.Stderr, "Value: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	r := bufio.NewReader(os.Stdin)
	v, err := r.ReadString('\n')
	if err != nil && v == "" {
		return "", err
	}
	return strings.TrimRight(v, "\r\n"), nil
}

func newSecretListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List secret names referenced by the config",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			var got []admin.SecretInfo
			if err := c.Get("/admin/secret", &got); err != nil {
				return err
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tUSED BY")
			for _, s := range got {
				fmt.Fprintf(tw, "%s\t%s\n", s.Name, strings.Join(s.UsedBy, ", "))
			}
			return tw.Flush()
		},
	}
}

func newSecretRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rm <name>",
		Short: "Delete a secret from the keychain",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			home, _ := os.UserHomeDir()
			c := adminclient.New(filepath.Join(home, ".mcp-gateway", "sock"))
			if err := c.Delete("/admin/secret/" + args[0]); err != nil {
				return err
			}
			fmt.Printf("secret %s deleted\n", args[0])
			return nil
		},
	}
}
```

- [ ] **Step 3: Register in `main.go`**

```go
root.AddCommand(newSecretCmd())
```

- [ ] **Step 4: Build, sanity check**

```bash
make build
./bin/mcp-gateway secret --help
```

- [ ] **Step 5: Commit**

```bash
git add cmd/mcp-gateway/secret.go cmd/mcp-gateway/main.go go.mod go.sum
git commit -m "feat(cli): secret set|list|rm subcommands (stdin value, no echo)"
```

---

## Phase 13 — CLI: start, stop, restart + status refactor

Goal: `start` spawns the daemon as a detached process if not already running; `stop` sends SIGTERM via pidfile; `restart` does both; `status` is rewritten to GET `/admin/status` over the UNIX socket (with TCP fallback).

### Task 13.1: start

**Files:**
- Create: `cmd/mcp-gateway/start.go`

- [ ] **Step 1: Implement**

```go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the daemon as a detached background process",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			pidPath := filepath.Join(home, ".mcp-gateway", "daemon.pid")
			if pid, ok := readPid(pidPath); ok && processAlive(pid) {
				return fmt.Errorf("daemon already running (pid=%d)", pid)
			}
			selfPath, err := os.Executable()
			if err != nil {
				return err
			}
			logPath := filepath.Join(home, ".mcp-gateway", "daemon.log")
			f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return err
			}
			cmd := exec.Command(selfPath, "daemon")
			cmd.Stdout = f
			cmd.Stderr = f
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				_ = f.Close()
				return err
			}
			_ = cmd.Process.Release()
			// Wait up to 5s for the socket to appear.
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err == nil {
					fmt.Printf("daemon started (pid=%d, log=%s)\n", cmd.Process.Pid, logPath)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon failed to come up within 5s; check %s", logPath)
		},
	}
}

func readPid(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, false
	}
	return pid, true
}

func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
```

### Task 13.2: stop, restart

**Files:**
- Create: `cmd/mcp-gateway/stop.go`, `restart.go`

- [ ] **Step 1: stop.go**

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

func newStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the running daemon (SIGTERM via pidfile)",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			pidPath := filepath.Join(home, ".mcp-gateway", "daemon.pid")
			pid, ok := readPid(pidPath)
			if !ok {
				return fmt.Errorf("no daemon running (no pidfile at %s)", pidPath)
			}
			if !processAlive(pid) {
				_ = os.Remove(pidPath)
				return fmt.Errorf("stale pidfile (pid=%d not running); removed", pid)
			}
			if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
				return err
			}
			// Wait up to 5s for the socket to vanish.
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(sock); err != nil && os.IsNotExist(err) {
					fmt.Printf("daemon stopped (pid=%d)\n", pid)
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			return fmt.Errorf("daemon did not exit within 5s (pid=%d)", pid)
		},
	}
}
```

- [ ] **Step 2: restart.go**

```go
package main

import (
	"github.com/spf13/cobra"
)

func newRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "restart",
		Short: "Stop then start the daemon",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := newStopCmd().RunE(cmd, args); err != nil {
				// Tolerate "no daemon running" — proceed to start.
			}
			return newStartCmd().RunE(cmd, args)
		},
	}
}
```

### Task 13.3: status — rewrite

**Files:**
- Modify: `cmd/mcp-gateway/main.go` (replace `newStatusCmd`)

- [ ] **Step 1: Replace**

```go
func newStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Print daemon status (via /admin/status over UNIX socket)",
		RunE: func(_ *cobra.Command, _ []string) error {
			home, _ := os.UserHomeDir()
			sock := filepath.Join(home, ".mcp-gateway", "sock")
			if _, err := os.Stat(sock); err != nil {
				return fmt.Errorf("daemon not running (no socket at %s)", sock)
			}
			c := adminclient.New(sock)
			var st admin.Status
			if err := c.Get("/admin/status", &st); err != nil {
				return err
			}
			fmt.Printf("daemon: OK (pid=%d, port=%d, version=%s, started=%s)\n",
				st.PID, st.HTTPPort, st.Version, st.StartedAt.Format(time.RFC3339))
			fmt.Printf("  servers: %d, tools: %d\n", st.NumServers, st.NumTools)
			fmt.Printf("  config:  %s\n", st.ConfigPath)
			fmt.Printf("  socket:  %s\n", st.SocketPath)
			return nil
		},
	}
}
```

Add imports: `time`, `internal/admin`, `internal/adminclient` (and remove now-unused `bytes`, `io`, `net/http` if status was the only consumer).

### Task 13.4: register all + build

- [ ] **Step 1: Update `newRootCmd`**

```go
root.AddCommand(newStartCmd())
root.AddCommand(newStopCmd())
root.AddCommand(newRestartCmd())
```

- [ ] **Step 2: Build + test**

```bash
make build
./bin/mcp-gateway --help
```

- [ ] **Step 3: Commit**

```bash
git add cmd/mcp-gateway/
git commit -m "feat(cli): start/stop/restart subcommands; status now hits /admin/status"
```

---

## Phase 14 — e2e + README update

### Task 14.1: admin e2e

**Files:**
- Create: `internal/daemon/admin_e2e_test.go`

- [ ] **Step 1: Implement (build tag e2e)**

```go
//go:build e2e

package daemon_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func unixHTTPClient(sock string) *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	return &http.Client{Transport: tr, Timeout: 5 * time.Second}
}

func TestE2E_AdminStatusOverUnix(t *testing.T) {
	tmp := t.TempDir()
	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	out, err := exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway").CombinedOutput()
	require.NoError(t, err, "go build: %s", string(out))

	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	cfg := `{"version":1,"daemon":{"http_port":17923,"log_level":"info"},"mcpServers":{}}`
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"), []byte(cfg), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gatewayBin, "daemon", "--home", home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	sock := filepath.Join(home, "sock")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	c := unixHTTPClient(sock)
	resp, err := c.Get("http://x/admin/status")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var st map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&st))
	assert.NotZero(t, st["pid"])
	assert.EqualValues(t, 17923, st["http_port"])
	assert.Equal(t, "0.2", st["version"])
}

func TestE2E_AdminNotOnTCP(t *testing.T) {
	tmp := t.TempDir()
	gatewayBin := filepath.Join(tmp, "mcp-gateway")
	out, err := exec.Command("go", "build", "-o", gatewayBin, "./cmd/mcp-gateway").CombinedOutput()
	require.NoError(t, err, "go build: %s", string(out))

	home := filepath.Join(tmp, "home")
	require.NoError(t, os.MkdirAll(home, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, "config.jsonc"),
		[]byte(`{"version":1,"daemon":{"http_port":17924,"log_level":"info"},"mcpServers":{}}`), 0o600))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, gatewayBin, "daemon", "--home", home)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	time.Sleep(1500 * time.Millisecond)

	resp, err := http.Get("http://127.0.0.1:17924/admin/status")
	if err == nil {
		defer resp.Body.Close()
		// Must NOT return 200 — admin path is not registered on TCP mux.
		assert.NotEqual(t, http.StatusOK, resp.StatusCode)
	}
}
```

### Task 14.2: full lifecycle e2e (using CLI binaries)

(Optional — extension of admin_e2e_test if time permits. Skip if Phase 15 timeline is tight.)

### Task 14.3: README — CLI section

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Replace the `## Configure` block** with a section that documents both the CLI and the JSONC approach. Add a new `## Day-to-day commands` section before `## Verify`:

```markdown
## Day-to-day commands

```bash
mcp-gateway start                       # spawn the daemon (detached)
mcp-gateway status                      # status from /admin/status
mcp-gateway list                        # all servers + state + token cost

# Add a server (prefix defaults to name)
mcp-gateway add github \
  --command npx --arg -y --arg @modelcontextprotocol/server-github \
  --env GITHUB_TOKEN='${secret:github_token}'

mcp-gateway disable github              # stop the child but keep config
mcp-gateway enable github               # start it again
mcp-gateway rm github                   # remove from config

# Secrets (stored in macOS Keychain; values never logged)
echo "$TOKEN" | mcp-gateway secret set github_token
mcp-gateway secret list
mcp-gateway secret rm github_token

mcp-gateway stop                        # SIGTERM via pidfile
mcp-gateway restart                     # stop + start
```

You can still hand-edit `~/.mcp-gateway/config.jsonc`; the daemon hot-reloads. The CLI just removes the need.
```

- [ ] **Step 2: Run e2e**

```bash
make e2e
```

Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/daemon/admin_e2e_test.go README.md
git commit -m "test(daemon): admin e2e + docs(readme): CLI section"
```

---

## Phase 15 — Final pass + tag v0.2.0-alpha

### Task 15.1: lint + tests

- [ ] **Step 1: Run all checks**

```bash
go vet ./...
go test -race -count=1 ./...
make e2e
make lint   # if golangci-lint installed locally
```

Expected: all green.

### Task 15.2: tag and push

- [ ] **Step 1: Tag**

```bash
git tag v0.2.0-alpha
git push origin main
git push origin v0.2.0-alpha
```

- [ ] **Step 2: Verify on GitHub**

```bash
gh repo view ayu5h-raj/mcp-gateway
gh run list --repo ayu5h-raj/mcp-gateway --limit 3
```

---

## v0.2 acceptance checklist

Before considering Plan 02 done, confirm by hand:

- [ ] `mcp-gateway start` brings the daemon up; `mcp-gateway start` again refuses with "daemon already running (pid=...)".
- [ ] `mcp-gateway stop` returns the socket path to non-existent in <5s.
- [ ] `mcp-gateway status` prints pid, port, num_servers, num_tools.
- [ ] `mcp-gateway list` shows servers with their state and token cost.
- [ ] `mcp-gateway add fs --command npx --arg -y --arg @modelcontextprotocol/server-filesystem --arg /tmp` adds a server; within 3 seconds `tools/list` over `/mcp` returns `fs__*` tools.
- [ ] `mcp-gateway disable fs` causes `tools/list` to return zero tools; `mcp-gateway enable fs` brings them back.
- [ ] `mcp-gateway rm fs` removes the server from config and the daemon stops the child.
- [ ] `echo "ghp_xxx" | mcp-gateway secret set github_token` succeeds; `grep -r 'ghp_xxx' ~/.mcp-gateway/` returns zero matches; `cat ~/.mcp-gateway/config.jsonc` does not contain the value.
- [ ] `mcp-gateway secret list` shows `github_token (used by github)` (after `add github --env GITHUB_TOKEN='${secret:github_token}'`).
- [ ] `curl --unix-socket ~/.mcp-gateway/sock http://x/admin/events` streams events when other commands are issued.
- [ ] `curl http://127.0.0.1:7823/admin/status` returns non-200 (admin endpoints not exposed on TCP).
- [ ] `bin/mgw-smoke --port 7823` still passes (no v0.1 regression).
- [ ] `make test`, `make e2e`, `go vet ./...` all pass.

---

## Known carry-overs into Plan 03 / 04

- **TUI (Plan 03)** — Bubble Tea. Connects to `/admin/events` (SSE) + polls `/admin/{status,servers,tools}`. 5 tabs: Dashboard, Server detail, Requests, Tools, Secrets.
- **Comment-preserving JSONC writer** — current `configwrite.Apply` strips comments on round-trip. Acceptable for v0.2 because the wizard / template (Plan 04) writes the comments and the CLI strips them on first mutation.
- **First-run wizard `mcp-gateway init`** (Plan 04) — interactive setup that writes a commented template config + offers to install launchd plist.
- **launchd plist** (Plan 04) — `~/Library/LaunchAgents/com.ayu5h-raj.mcp-gateway.plist` for auto-start on login.
- **goreleaser, brew tap, install.sh** (Plan 04) — proper releases.
- **Linux/Windows secret backends** — code-path is ready (just `Backend` interface implementations), gated by build tags.
- **Sampling/elicitation forwarding, OAuth passthrough, HTTP/SSE downstream MCPs, per-client tool scoping** — Plan 05+.
