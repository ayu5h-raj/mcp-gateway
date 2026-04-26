package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// refuseIfLaunchdManaged should return nil for a PID that launchd doesn't
// know about (covers all cases on non-Darwin and the common case on Darwin
// where a manually-started daemon is being stopped).
func TestRefuseIfLaunchdManaged_UnknownPidReturnsNil(t *testing.T) {
	// Pick a pid that's exceedingly unlikely to be the launchd-tracked
	// mcp-gateway daemon: our own. (We are the test binary, not the daemon.)
	if err := refuseIfLaunchdManaged(os.Getpid()); err != nil {
		t.Fatalf("refuseIfLaunchdManaged(self) should be nil for non-launchd pid, got: %v", err)
	}
}

// processAlive should report true for a live child and false after it exits.
// stop's polling loop depends on this returning false promptly after death.
func TestProcessAlive_FlipsAfterChildExits(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	if !processAlive(pid) {
		t.Fatalf("expected pid=%d alive immediately after Start", pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("kill: %v", err)
	}
	_ = cmd.Wait() // reap so processAlive's Signal(0) sees ESRCH, not EPERM-on-zombie

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("processAlive(%d) still true 2s after Kill+Wait", pid)
}
