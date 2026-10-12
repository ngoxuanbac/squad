package local

// A process tree is started so that the whole tree can be stopped, the same way on Windows (a job
// object) and on Linux (a process group led by a guard, proctree_unix.go).

import (
	"os/exec"
	"time"
)

// Tree is a started process and every process it starts.
type Tree struct {
	cmd *exec.Cmd
	sys treeSys
}

// startTree starts cmd in a tree of its own.
func startTree(cmd *exec.Cmd) (*Tree, error) {
	t := &Tree{cmd: cmd}
	if err := t.start(); err != nil {
		return nil, err
	}
	return t, nil
}

// Pid is the root process id.
func (t *Tree) Pid() int { return t.cmd.Process.Pid }

// Kill stops the tree: politely first where the OS has a way, then for good after grace.
// done is closed when the root process has been waited for.
func (t *Tree) Kill(grace time.Duration, done <-chan struct{}) {
	t.terminate()
	select {
	case <-done:
	case <-time.After(grace):
	}
	t.kill()
}

// Release frees what the tree holds (a job handle) once the root process has exited.
func (t *Tree) Release() { t.release() }
