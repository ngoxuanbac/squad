package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bacnx/squad/internal/core"
	"github.com/bacnx/squad/internal/server"
	"github.com/bacnx/squad/internal/view"
)

// tail: a worker log as readable lines, once or followed.

// followStop ends a follow when closed (tests); nil follows until the process is stopped.
var followStop chan struct{}

func tail(id string, n int, follow, asView, raw bool) error {
	path := core.WorkerLog(server.Dir(), id)
	if raw {
		b, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		fmt.Println(strings.Join(lines[max(0, len(lines)-n):], "\n"))
		return nil
	}
	lines, err := view.ReadTail(path, n)
	if err != nil {
		return err
	}
	if asView {
		return printJSON(map[string]any{"version": view.TailVersion, "lines": lines})
	}
	printTail(lines)
	if !follow {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	var buf []byte
	chunk := make([]byte, 64<<10)
	for {
		k, _ := f.Read(chunk)
		if k == 0 {
			select {
			case <-followStop:
				return nil
			case <-time.After(300 * time.Millisecond):
			}
			continue
		}
		buf = append(buf, chunk[:k]...)
		for {
			i := strings.IndexByte(string(buf), '\n')
			if i < 0 {
				break
			}
			printTail(view.ParseLine(buf[:i]))
			buf = buf[i+1:]
		}
	}
}
