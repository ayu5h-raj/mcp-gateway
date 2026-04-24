package supervisor

import (
	"context"
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
	// Hook, if non-nil, is called from the supervisor's manager goroutines
	// whenever a server transitions state. prev and next are the state
	// before/after the transition; err is non-nil for crash transitions.
	// Hook is called AFTER releasing the internal mutex.
	Hook func(name string, prev, next State, err error)
}

// server is the internal per-name state; goroutine-owned (one "manager" goroutine per name).
type server struct {
	spec    ServerSpec
	desired bool // true = should be running; false = user removed it

	state       State
	restarts    int
	started     time.Time
	lastErr     error
	nextStartAt time.Time // earliest permissible restart time; zero = immediate

	proc   *Process
	cancel context.CancelFunc

	backoff *Backoff
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
	var earliest time.Time
	now := time.Now()
	for name, srv := range s.servers {
		switch {
		case !srv.desired && srv.proc == nil && srv.state != StateStopped:
			srv.state = StateStopped
		case !srv.desired && srv.proc != nil:
			toKill = append(toKill, name)
		case srv.desired && srv.state == StateDisabled:
			// stay disabled; user must explicitly re-set
		case srv.desired && srv.proc == nil && srv.state != StateStarting:
			// Respect backoff: only start when the server's nextStartAt has arrived.
			if !srv.nextStartAt.IsZero() && now.Before(srv.nextStartAt) {
				if earliest.IsZero() || srv.nextStartAt.Before(earliest) {
					earliest = srv.nextStartAt
				}
				continue
			}
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

	// If at least one server is waiting out a backoff window, schedule a wake
	// for the earliest deadline so a spurious wake() doesn't defeat the wait.
	if !earliest.IsZero() {
		d := time.Until(earliest)
		if d > 0 {
			go func() {
				t := time.NewTimer(d)
				defer t.Stop()
				select {
				case <-t.C:
					s.wake()
				case <-ctx.Done():
				}
			}()
		}
	}
}

func (s *Supervisor) startServer(parentCtx context.Context, name string) {
	s.mu.Lock()
	srv, ok := s.servers[name]
	if !ok || !srv.desired {
		s.mu.Unlock()
		return
	}
	prevState := srv.state
	srv.state = StateStarting
	spec := srv.spec
	logDir := s.opts.LogDir
	s.mu.Unlock()
	if s.opts.Hook != nil {
		s.opts.Hook(name, prevState, StateStarting, nil)
	}

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
		prev2 := srv.state
		srv.restarts++
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			srv.nextStartAt = time.Time{}
			s.mu.Unlock()
			if s.opts.Hook != nil {
				s.opts.Hook(name, prev2, StateDisabled, err)
			}
			return
		}
		srv.state = StateErrored
		d := srv.backoff.Next()
		srv.nextStartAt = time.Now().Add(d)
		s.mu.Unlock()
		if s.opts.Hook != nil {
			s.opts.Hook(name, prev2, StateErrored, err)
		}
		s.wake()
		return
	}

	s.mu.Lock()
	srv.proc = p
	srv.cancel = cancel
	srv.started = time.Now()
	prev3 := srv.state
	srv.state = StateRunning
	srv.nextStartAt = time.Time{}
	s.mu.Unlock()
	if s.opts.Hook != nil {
		s.opts.Hook(name, prev3, StateRunning, nil)
	}

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
			prevExit := srv.state
			srv.state = StateStopped
			s.mu.Unlock()
			if s.opts.Hook != nil {
				s.opts.Hook(name, prevExit, StateStopped, nil)
			}
			s.wake()
			return
		}
		// If the process ran long enough to be considered stable, reset the
		// restart counter so transient later failures don't accumulate.
		if time.Since(srv.started) > 30*time.Second {
			srv.restarts = 0
			srv.backoff.Reset()
		}
		// Unexpected exit.
		srv.lastErr = waitErr
		srv.restarts++
		prevCrash := srv.state
		if srv.restarts >= s.opts.MaxRestartAttempts {
			srv.state = StateDisabled
			srv.nextStartAt = time.Time{}
			s.mu.Unlock()
			if s.opts.Hook != nil {
				s.opts.Hook(name, prevCrash, StateDisabled, waitErr)
			}
			return
		}
		srv.state = StateErrored
		d := srv.backoff.Next()
		srv.nextStartAt = time.Now().Add(d)
		s.mu.Unlock()
		if s.opts.Hook != nil {
			s.opts.Hook(name, prevCrash, StateErrored, waitErr)
		}
		s.wake()
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
		remaining := false
		for _, srv := range s.servers {
			if srv.proc != nil {
				remaining = true
				break
			}
		}
		s.mu.Unlock()
		if !remaining {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	// TODO(logger): inject *slog.Logger on SupervisorOpts and log a shutdown
	// timeout here (some children may still be running in their process groups).
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
