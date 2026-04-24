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
