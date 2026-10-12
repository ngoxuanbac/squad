//go:build windows

package cli

import (
	"errors"
	"io"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// dialMessagingSocket opens Claude's named pipe for writing, waiting a moment while it is busy.
func dialMessagingSocket(path string) (io.WriteCloser, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err == nil || !errors.Is(err, windows.ERROR_PIPE_BUSY) || time.Now().After(deadline) {
			return f, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
