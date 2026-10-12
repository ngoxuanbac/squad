//go:build !windows

package cli

import (
	"io"
	"net"
	"time"
)

// dialMessagingSocket connects to Claude's unix socket.
func dialMessagingSocket(path string) (io.WriteCloser, error) {
	c, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, err
	}
	_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return c, nil
}
