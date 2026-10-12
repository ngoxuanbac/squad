//go:build windows

package local

import (
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// treeSys is the job object every process of the tree belongs to. KILL_ON_JOB_CLOSE: when the
// daemon goes away, its workers go with it, so none is left running without anyone reading it.
type treeSys struct {
	job windows.Handle
}

func (t *Tree) start() error {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return err
	}
	t.cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := t.cmd.Start(); err != nil {
		windows.CloseHandle(job)
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(t.cmd.Process.Pid))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, h)
		windows.CloseHandle(h)
	}
	if err != nil {
		// The tree cannot be tracked: kill the root rather than leave it unmanaged.
		_ = t.cmd.Process.Kill()
		windows.CloseHandle(job)
		return err
	}
	t.sys.job = job
	return nil
}

// terminate: Windows has no SIGTERM for a console-less process; Ctrl+Break to its group is the
// nearest polite stop and many programs ignore it, so Kill follows after the grace period.
func (t *Tree) terminate() {
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(t.cmd.Process.Pid))
}

func (t *Tree) kill() {
	if t.sys.job != 0 {
		_ = windows.TerminateJobObject(t.sys.job, 1)
	} else {
		_ = t.cmd.Process.Kill()
	}
}

func (t *Tree) release() {
	if t.sys.job != 0 {
		windows.CloseHandle(t.sys.job)
		t.sys.job = 0
	}
}

// Detach makes cmd outlive the process that starts it (the daemon started by a CLI command),
// also when that process runs inside a job that kills its members on close.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_BREAKAWAY_FROM_JOB,
		HideWindow:    true,
	}
}

// DetachNoBreakaway is Detach for a job that does not allow breaking away.
func DetachNoBreakaway(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		HideWindow:    true,
	}
}

// Alive says whether a process with pid exists and has not exited.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == 259 // STILL_ACTIVE
}
