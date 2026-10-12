package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	paseoext "github.com/bacnx/squad/extensions/paseo"
	"github.com/bacnx/squad/internal/server"
)

// setup paseo: the embedded plugin (extensions/paseo) written out and installed in Paseo.

// paseoBin is the Paseo CLI: on PATH, else the one the Paseo desktop app ships.
func paseoBin() string {
	if p, err := exec.LookPath("paseo"); err == nil {
		return p
	}
	var cands []string
	switch runtime.GOOS {
	case "windows":
		cands = append(cands, filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Paseo", "resources", "bin", "paseo.cmd"))
	case "darwin":
		cands = append(cands, "/Applications/Paseo.app/Contents/Resources/bin/paseo")
	default:
		cands = append(cands, "/opt/Paseo/resources/bin/paseo", "/usr/lib/paseo/resources/bin/paseo")
	}
	for _, c := range cands {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

// setupPaseo writes the embedded plugin to ~/.squad/paseo, naming this binary in
// server/installed.ts, and installs that directory in the Paseo daemon.
func setupPaseo() error {
	bin := paseoBin()
	if bin == "" {
		return errors.New("paseo not found: install Paseo (https://paseo.sh) or put its CLI on PATH")
	}
	dst := paseoDir(server.Dir())
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	exe := self()
	err := fs.WalkDir(paseoext.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		out := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(out, 0o700)
		}
		b, err := fs.ReadFile(paseoext.FS, p)
		if err != nil {
			return err
		}
		if p == paseoext.InstalledFile {
			q, _ := json.Marshal(exe)
			text := strings.Replace(string(b), paseoext.InstalledPlaceholder, "export const squadPath = "+string(q)+";", 1)
			if text == string(b) {
				return fmt.Errorf("%s has no placeholder", p)
			}
			b = []byte(text)
		}
		return os.WriteFile(out, b, 0o600)
	})
	if err != nil {
		return err
	}
	if paseoHasSquad(bin) {
		_ = exec.Command(bin, "plugin", "remove", "squad").Run()
	}
	cmd := exec.Command(bin, "plugin", "install", dst)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("paseo plugin install: %w", err)
	}
	fmt.Println("Squad plugin installed in Paseo from", dst, "- reload the Paseo app to see it.")
	return nil
}

// paseoDir is where setup paseo writes the plugin before installing it in Paseo.
func paseoDir(dir string) string { return filepath.Join(dir, "paseo") }
