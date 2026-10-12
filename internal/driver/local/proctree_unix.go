//go:build !windows

package local

import (
	"os/exec"
	"syscall"
)

type treeSys struct{}

func (t *Tree) start() error {
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	diesWithParent(t.cmd.SysProcAttr)
	return t.cmd.Start()
}

func (t *Tree) terminate() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM) }
func (t *Tree) kill()      { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }
func (t *Tree) release()   {}

// Detach makes cmd outlive the process that starts it (the daemon started by a CLI command).
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// Alive says whether a process with pid exists.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// DetachNoBreakaway is Detach (job objects are a Windows matter).
func DetachNoBreakaway(cmd *exec.Cmd) { Detach(cmd) }
