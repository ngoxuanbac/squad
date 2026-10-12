//go:build !windows

package local

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// On Linux the tree is a process group whose leader is a guard: this binary run again as
// `<self> __squad_guard <cmd> <args…>`. The guard starts the command in its group and holds the
// read end of a pipe whose write end only the daemon has; when the daemon is gone, however it went,
// the pipe ends and the guard kills the group, the processes the worker started included (what
// the job object's KILL_ON_JOB_CLOSE does on Windows).

const guardArg = "__squad_guard"

func init() {
	if len(os.Args) > 2 && os.Args[1] == guardArg {
		os.Exit(guard(os.Args[2:]))
	}
}

// treeSys is the daemon's end of the guard's pipe.
type treeSys struct{ daemon *os.File }

func (t *Tree) start() error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer r.Close()
	t.cmd.Args = append([]string{self, guardArg, t.cmd.Path}, t.cmd.Args[1:]...)
	t.cmd.Path = self
	t.cmd.ExtraFiles = []*os.File{r} // fd 3
	t.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := t.cmd.Start(); err != nil {
		w.Close()
		return err
	}
	t.sys.daemon = w
	return nil
}

func (t *Tree) terminate() { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGTERM) }
func (t *Tree) kill()      { _ = syscall.Kill(-t.cmd.Process.Pid, syscall.SIGKILL) }

// release ends what is left of the group once its leader exited.
func (t *Tree) release() {
	t.kill()
	if t.sys.daemon != nil {
		t.sys.daemon.Close()
		t.sys.daemon = nil
	}
}

// guard runs argv in its process group and kills the group when fd 3 ends; its exit code is the
// command's.
func guard(argv []string) int {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "squad guard:", err)
		return 127
	}
	go func() {
		_, _ = io.Copy(io.Discard, os.NewFile(3, "daemon"))
		_ = syscall.Kill(0, syscall.SIGKILL)
	}()
	var ee *exec.ExitError
	if err := cmd.Wait(); errors.As(err, &ee) {
		return ee.ExitCode()
	} else if err != nil {
		return 1
	}
	return 0
}

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
