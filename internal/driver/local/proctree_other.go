//go:build !linux && !windows

package local

import "syscall"

// diesWithParent: macOS has no parent-death signal; a worker ends when its stdin closes.
func diesWithParent(*syscall.SysProcAttr) {}
