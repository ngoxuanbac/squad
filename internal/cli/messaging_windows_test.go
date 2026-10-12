//go:build windows

package cli

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// dialMessagingSocket reaches a real named pipe, as Claude's messaging socket is on Windows.
func TestDialMessagingSocketPipe(t *testing.T) {
	path := fmt.Sprintf(`\\.\pipe\squad-test-%d-%d`, os.Getpid(), time.Now().UnixNano())
	name, _ := windows.UTF16PtrFromString(path)
	h, err := windows.CreateNamedPipe(name, windows.PIPE_ACCESS_INBOUND, windows.PIPE_TYPE_BYTE|windows.PIPE_WAIT, 1, 4096, 4096, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		defer windows.CloseHandle(h)
		if err := windows.ConnectNamedPipe(h, nil); err != nil && err != windows.ERROR_PIPE_CONNECTED {
			got <- "connect: " + err.Error()
			return
		}
		var all []byte
		buf := make([]byte, 4096)
		for {
			var n uint32
			if err := windows.ReadFile(h, buf, &n, nil); err != nil || n == 0 {
				break
			}
			all = append(all, buf[:n]...)
		}
		got <- string(all)
	}()
	time.Sleep(50 * time.Millisecond)
	s := &mcpServer{getenv: func(k string) string {
		return map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": path}[k]
	}}
	if err := s.nudge(); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if !strings.Contains(line, "[squad] wake #1") {
			t.Fatalf("pipe got %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing on the pipe")
	}
	if _, err := dialMessagingSocket(path + "-none"); err == nil {
		t.Fatal("dialed a pipe that does not exist")
	}
}
