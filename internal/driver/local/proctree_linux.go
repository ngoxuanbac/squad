//go:build linux

package local

import "syscall"

// diesWithParent has the kernel kill the root of the tree when the daemon dies, however it dies
// (on Windows the job object does that). The signal follows the OS thread that started the
// process; squad locks no goroutine to a thread, so Go never ends that thread before the daemon.
func diesWithParent(a *syscall.SysProcAttr) { a.Pdeathsig = syscall.SIGKILL }
