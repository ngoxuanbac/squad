//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

func socketPath() string {
	return fmt.Sprintf(`\\.\pipe\fakeclaude-%d-%d`, os.Getpid(), time.Now().UnixNano())
}

// listen serves the named pipe at path, as Claude's messaging socket is on Windows: each client
// is read by serve.
func listen(path string, serve func(io.Reader)) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	create := func() (windows.Handle, error) {
		return windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT,
			windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, nil)
	}
	h, err := create() // the first instance exists before anyone dials
	if err != nil {
		return err
	}
	go func() {
		for {
			if err := windows.ConnectNamedPipe(h, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
				windows.CloseHandle(h)
			} else {
				go func(f *os.File) { defer f.Close(); serve(f) }(os.NewFile(uintptr(h), path))
			}
			if h, err = create(); err != nil {
				return
			}
		}
	}()
	return nil
}
