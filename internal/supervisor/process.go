package supervisor

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// SpawnConfig describes how to launch a child process.
type SpawnConfig struct {
	Name       string
	Command    string
	Args       []string
	Env        map[string]string
	StderrPath string // if set, child stderr is tee'd to this file
}

// Process is a running child; wraps exec.Cmd and the three stdio pipes.
type Process struct {
	Name   string
	Cmd    *exec.Cmd
	Stdin  io.WriteCloser
	Stdout io.ReadCloser
	doneCh chan struct{}
	err    error
}

// Spawn launches a new child and returns a Process handle.
// The child runs in its own process group so we can cleanly signal the whole tree.
func Spawn(ctx context.Context, sc SpawnConfig) (*Process, error) {
	if sc.Command == "" {
		return nil, fmt.Errorf("spawn %s: empty command", sc.Name)
	}
	cmd := exec.CommandContext(ctx, sc.Command, sc.Args...)

	// Env: inherit the parent env (users sometimes rely on PATH), then overlay ours.
	env := os.Environ()
	for k, v := range sc.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env

	// Own process group; allows Kill to signal the whole tree.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn %s: stdin: %w", sc.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("spawn %s: stdout: %w", sc.Name, err)
	}

	// stderr → file if requested; otherwise discard (we don't want to flood daemon output).
	var stderrWriter io.Writer = io.Discard
	var stderrFile *os.File
	if sc.StderrPath != "" {
		f, err := os.OpenFile(sc.StderrPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("spawn %s: open stderr log: %w", sc.Name, err)
		}
		stderrFile = f
		stderrWriter = f
	}
	cmd.Stderr = stderrWriter

	if err := cmd.Start(); err != nil {
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
		return nil, fmt.Errorf("spawn %s: start: %w", sc.Name, err)
	}

	p := &Process{
		Name:   sc.Name,
		Cmd:    cmd,
		Stdin:  stdin,
		Stdout: stdout,
		doneCh: make(chan struct{}),
	}

	go func() {
		p.err = cmd.Wait()
		if stderrFile != nil {
			_ = stderrFile.Close()
		}
		close(p.doneCh)
	}()

	return p, nil
}

// Done is closed when the child exits.
func (p *Process) Done() <-chan struct{} { return p.doneCh }

// Err returns the process's exit error (nil if clean).
func (p *Process) Err() error {
	select {
	case <-p.doneCh:
		return p.err
	default:
		return nil
	}
}

// Kill terminates the process group with SIGTERM.
// SIGKILL fallback is left to context cancellation or the supervisor's own timeout.
func (p *Process) Kill() error {
	if p.Cmd == nil || p.Cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(p.Cmd.Process.Pid)
	if err != nil {
		// fall back to PID
		pgid = p.Cmd.Process.Pid
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	return nil
}
