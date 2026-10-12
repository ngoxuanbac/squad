//go:build !windows

package cli

import (
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// dialMessagingSocket reaches a real unix socket, as Claude's messaging socket is on Unix.
func TestDialMessagingSocketUnix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msg.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- err.Error()
			return
		}
		b, _ := io.ReadAll(c)
		got <- string(b)
	}()
	s := &mcpServer{getenv: func(k string) string {
		return map[string]string{"CLAUDE_CODE_MESSAGING_SOCKET": path}[k]
	}}
	if err := s.nudge(); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-got:
		if !strings.Contains(line, "[squad] wake #1") {
			t.Fatalf("socket got %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing on the socket")
	}
	if _, err := dialMessagingSocket(path + "-none"); err == nil {
		t.Fatal("dialed a socket that does not exist")
	}
}
