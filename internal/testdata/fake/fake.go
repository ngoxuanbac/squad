// Package fake builds the fake claude (internal/core/testdata/fakeclaude) for a package's tests.
// It lives under testdata so that `go test ./...` does not count it as a package of its own.
package fake

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Build builds the fake claude into a new temporary directory: its path, and the removal of
// that directory once the tests are done.
func Build() (path string, done func()) {
	dir, err := os.MkdirTemp("", "squad-fake")
	if err != nil {
		panic(err)
	}
	path = filepath.Join(dir, "claude")
	if runtime.GOOS == "windows" {
		path += ".exe"
	}
	_, here, _, _ := runtime.Caller(0)
	src := filepath.Join(filepath.Dir(here), "..", "..", "core", "testdata", "fakeclaude")
	if out, err := exec.Command("go", "build", "-o", path, src).CombinedOutput(); err != nil {
		panic(string(out))
	}
	return path, func() { os.RemoveAll(dir) }
}
