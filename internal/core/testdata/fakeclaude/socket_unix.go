//go:build !windows

package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

func socketPath() string {
	return filepath.Join(os.TempDir(), fmt.Sprintf("fakeclaude-%d.sock", os.Getpid()))
}

// listen serves the unix socket at path, as Claude's messaging socket is: each client is read by
// serve.
func listen(path string, serve func(io.Reader)) error {
	os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return nil
}
