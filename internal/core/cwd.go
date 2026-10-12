package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Where a worker or a taskforce may work: inside the team root or a git worktree of its repo.

// bounded resolves cwd (relative to from) and refuses a directory outside root, unless it is a
// git worktree of root's repository.
func (e *Engine) bounded(root, from, cwd string) (string, error) {
	dir := cwd
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(from, dir)
	}
	dir = filepath.Clean(dir)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", errf("invalid", "cwd %s is not a directory", dir)
	}
	if within(root, dir) {
		return dir, nil
	}
	for _, r := range e.allowedRoots {
		if within(filepath.Clean(r), dir) {
			return dir, nil
		}
	}
	out, err := exec.Command("git", "-C", root, "worktree", "list", "--porcelain").Output()
	if err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if wt, ok := strings.CutPrefix(strings.TrimSpace(line), "worktree "); ok && within(filepath.Clean(filepath.FromSlash(wt)), dir) {
				return dir, nil
			}
		}
	}
	return "", denyf("bounds", "cwd.bounds", "cwd %s is outside the team root %s, is not a git worktree of it, and is in none of spawn.allowed_roots", dir, root)
}

func within(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// cleanPath is a directory as squad compares them: cleaned, and case-folded on Windows.
func cleanPath(p string) string {
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}
