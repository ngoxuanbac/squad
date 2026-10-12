//go:build !windows

package local

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// Detach puts the daemon in a session of its own: the shell or agent that started it can end
// without taking it along.
func TestDetachNewSession(t *testing.T) {
	cmd := exec.Command(fakeClaude, "hang")
	Detach(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	sid, err := unix.Getsid(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if mine, _ := unix.Getsid(0); sid == mine || sid != cmd.Process.Pid {
		t.Fatalf("session %d, ours %d, its pid %d", sid, mine, cmd.Process.Pid)
	}
}
