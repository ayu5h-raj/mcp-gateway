package supervisor

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"
)

// ServerSpec is the supervisor's input — derived from config.Server at each reconcile.
type ServerSpec struct {
	Name    string
	Command string
	Args    []string
	Env     map[string]string
}

// Status is an immutable snapshot of a server's runtime state.
type Status struct {
	Name         string
	State        State
	RestartCount int
	StartedAt    time.Time
	LastError    error
}

// Supervisor orchestrates a set of child processes identified by name.
// Call Set/Remove to declare desired state; Run enforces it.
type Supervisor struct {
	opts SupervisorOpts

	mu      sync.Mutex
	servers map[string]*server
	notify  chan struct{} // wakes the run loop
	done    chan struct{}
}

// SupervisorOpts tunes restart behavior and where stderr logs land.
type SupervisorOpts struct {
	LogDir             string
	MaxRestartAttempts int
	BackoffMaxSeconds  int
}

// server is the internal per-name state; goroutine-owned (one "manager" goroutine per name).
type server struct {
	spec    ServerSpec
	desired bool // true = should be running; false = user removed it

	state    State
	restarts int
	started  time.Time
	lastErr  error

	proc   *Process
	cancel context.CancelFunc

	backoff *Backoff
	done    chan struct{}
}

// New creates a Supervisor. It is not started until you call Run.
func New(opts SupervisorOpts) *Supervisor {
	if opts.MaxRestartAttempts == 0 {
		opts.MaxRestartAttempts = 5
	}
	if opts.BackoffMaxSeconds == 0 {
		opts.BackoffMaxSeconds = 60
	}
	return &Supervisor{
		opts:    opts,
		servers: map[string]*server{},
		notify:  make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
}

// Run blocks until ctx is cancelled. Reconciles on wakeup; idle otherwise.
func (s *Supervisor) Run(ctx context.Context) {
	defer close(s.done)
	for {
		s.reconcile(ctx)
		select {
		case <-ctx.Done():
			s.shutdownAll()
			return
		case <-s.notify:
		}
	}
}

// Set declares the desired state for a server. Creates or updates in place.
func (s *Supervisor) Set(name string, spec ServerSpec) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		srv = &server{
			state:   StateStopped,
			backoff: NewBackoff(s.opts.BackoffMaxSeconds),
		}
		s.servers[name] = srv
	}
	specChanged := !specEqual(srv.spec, spec)
	srv.spec = spec
	srv.desired = true
	if specChanged && (srv.state == StateRunning || srv.state == StateStarting) {
		srv.state = StateRestarting
	}
	s.wake()
}

// Remove marks a server for teardown; it transitions to Stopped when its child exits.
func (s *Supervisor) Remove(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return
	}
	srv.desired = false
	s.wake()
}

// Status returns a snapshot for name (zero-valued if unknown).
func (s *Supervisor) Status(name string) Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, ok := s.servers[name]
	if !ok {
		return Status{Name: name}
	}
	return Status{
		Name:         name,
		State:        srv.state,
		RestartCount: srv.restarts,
		StartedAt:    srv.started,
		LastError:    srv.lastErr,
	}
}

// Process returns the live Process for name, or nil.
func (s *Supervisor) Process(name string) *Process {
	s.mu.Lock()
	defer s.mu.Unlock()
	if srv, ok := s.servers[name]; ok && srv.proc != nil {
		return srv.proc
	}
	return nil
}

// List returns current snapshot for all servers.
func (s *Supervisor) List() []Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Status, 0, len(s.servers))
	for name, srv := range s.servers {
		out = append(out, Status{
			Name:         name,
			State:        srv.state,
			RestartCount: srv.restarts,
			StartedAt:    srv.started,
			LastError:    srv.lastErr,
		})
	}
	return out
}

func (s *Supervisor) wake() {
	select {
	case s.notify <- struct{}{}:
	default:
	}
}

func (s *Supervisor) reconcile(ctx context.Context) {
	s.mu.Lock()
	var toStart []string
	var toKill []string
	for name, srv := range s.servers {
		switch {
		case !srv.desired && srv.proc == nil && srv.state != StateStopped:
			srv.state = StateStopped
		case !srv.desired && srv.proc != nil:
			toKill = append(toKill, name)
		case srv.desired && srv.state == StateDisabled:
			// stay disabled; user must explicitly re-set
		case srv.desired && srv.proc == nil && srv.state != StateStopped && srv.state != StateStarting:
			toStart = append(toStart, name)
		case srv.desired && srv.proc == nil && srv.state == StateStopped:
			toStart = append(toStart, name)
		case srv.desired && srv.state == StateRestarting && srv.proc != nil:
			toKill = append(toKill, name)
		}
	}
	s.mu.Unlock()

	for _, n := range toKill {
		s.killServer(n)
	}
	for _, n := range toStart {
		s.startServer(ctx, n)
	}
}

func (s *Supervisor) startServer(parentCtx context.Context, name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok || !srv.desired {
		s.mu.Unlock()
		return
	}
	srv.state = StateStarting
	spec := srv.spec
	logDir := s.opts.LogDir
	s.mu.Unlock()

	childCtx, cancel := context.WithCancel(parentCtx)
	p, err := Spawn(childCtx, SpawnConfig{
		Name:       spec.Name,
		Command:    spec.Command,
		Args:       spec.Args,
		Env:        spec.Env,
		StderrPath: filepath.Join(logDir, name+".log"),
	})
	if err != nil {
		cancel()
		s.mu.Lock()
		srv.lastErr = err
		srv.state = StateErrored
		srv.restarts++
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			s.mu.Unlock()
			return
		}
		// schedule backoff retry via wake after sleep
		d := srv.backoff.Next()
		s.mu.Unlock()
		go func() {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				s.wake()
			case <-parentCtx.Done():
			}
		}()
		return
	}

	s.mu.Lock()
	srv.proc = p
	srv.cancel = cancel
	srv.started = time.Now()
	srv.state = StateRunning
	s.mu.Unlock()

	// Watch for exit.
	go func() {
		<-p.Done()
		waitErr := p.Err()
		s.mu.Lock()
		srv.proc = nil
		if srv.cancel != nil {
			srv.cancel()
			srv.cancel = nil
		}
		if !srv.desired {
			srv.state = StateStopped
			s.mu.Unlock()
			s.wake()
			return
		}
		// Unexpected exit.
		srv.lastErr = waitErr
		srv.restarts++
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			s.mu.Unlock()
			return
		}
		srv.state = StateErrored
		d := srv.backoff.Next()
		s.mu.Unlock()
		go func() {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				s.wake()
			case <-parentCtx.Done():
			}
		}()
	}()
}

func (s *Supervisor) killServer(name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok || srv.proc == nil {
		s.mu.Unlock()
		return
	}
	p := srv.proc
	s.mu.Unlock()
	_ = p.Kill()
	// The goroutine registered in startServer handles post-exit state.
}

func (s *Supervisor) shutdownAll() {
	s.mu.Lock()
	var procs []*Process
	for _, srv := range s.servers {
		if srv.proc != nil {
			procs = append(procs, srv.proc)
		}
	}
	s.mu.Unlock()
	for _, p := range procs {
		_ = p.Kill()
	}
	// Give children a moment to exit.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		any := false
		for _, srv := range s.servers {
			if srv.proc != nil {
				any = true
				break
			}
		}
		s.mu.Unlock()
		if !any {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = errors.New("shutdown timed out") // placeholder for logger injection later
}

func specEqual(a, b ServerSpec) bool {
	if a.Name != b.Name || a.Command != b.Command {
		return false
	}
	if len(a.Args) != len(b.Args) {
		return false
	}
	for i := range a.Args {
		if a.Args[i] != b.Args[i] {
			return false
		}
	}
	if len(a.Env) != len(b.Env) {
		return false
	}
	for k, v := range a.Env {
		if b.Env[k] != v {
			return false
		}
	}
	return true
}
