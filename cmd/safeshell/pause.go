package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/install"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// pausedFile exists while checking is paused. The shell hooks compute the same
// path (XDG_STATE_HOME or ~/.local/state, then safeshell/off) and test it with
// the shell's own [[ -e ]], so a paused SafeShell starts no process.
func pausedFile() string {
	return filepath.Join(xdg.Dir("XDG_STATE_HOME", ".local", "state"), "off")
}

func paused() bool {
	_, err := os.Stat(pausedFile())
	return err == nil
}

// runPause is `safeshell deactivate` (off true) and `safeshell activate`.
func runPause(off bool, stdout, stderr io.Writer) int {
	p := pausedFile()
	if off {
		if paused() {
			fmt.Fprintln(stdout, "SafeShell is already off in every terminal. Turn it back on with: safeshell activate")
			return 0
		}
		if err := touchFile(p); err != nil {
			fmt.Fprintln(stderr, "safeshell deactivate: cannot save the setting:", err)
			return 1
		}
		fmt.Fprintln(stdout, "SafeShell is off in every terminal. Commands now run without a check. Turn it back on with: safeshell activate")
		return 0
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintln(stderr, "safeshell activate: cannot remove", p+":", err, "\nDelete that file by hand, then try again.")
		return 1
	}
	fmt.Fprintln(stdout, "SafeShell is on. Every command is checked again.")
	if sh, err := shellFor(); err != nil || !install.Installed(sh) {
		fmt.Fprintln(stdout, "The hook is not installed, so nothing is checked in your terminals yet. Run: safeshell install")
	}
	return 0
}
