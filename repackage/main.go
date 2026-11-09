// TiinyOS Linux, one executable.
//
// The payload holds only this project's files: the launcher, main.js,
// the Linux pcsvr, and the fetch scripts. On start it unpacks them
// under ~/.local/share/tiinyos/launcher when they changed, then execs
// linux/tiinyos.sh, which downloads the official TiinyOS app (and
// Electron) on first run and on every newer release.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		fatal(err)
	}
	root := filepath.Join(home, ".local", "share", "tiinyos")
	launcher := filepath.Join(root, "launcher")
	if err := ensurePayload(launcher); err != nil {
		fatal(err)
	}
	script := filepath.Join(launcher, "linux", "tiinyos.sh")
	self, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	env := append(os.Environ(),
		"TIINY_LAUNCH="+self,
		"TIINY_DATA_DIR="+filepath.Join(root, "data"),
		"TIINY_APP_DIR="+filepath.Join(root, "app"),
	)
	args := append([]string{script}, os.Args[1:]...)
	fatal(syscall.Exec(script, args, env))
}

func ensurePayload(dir string) error {
	sum := sha256.Sum256(payload)
	stamp := hex.EncodeToString(sum[:])
	marker := filepath.Join(dir, ".payload")
	if existing, err := os.ReadFile(marker); err == nil && string(existing) == stamp {
		return nil
	}
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := extractPayload(dir); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(stamp), 0o644)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "tiinyos:", err)
	os.Exit(1)
}
