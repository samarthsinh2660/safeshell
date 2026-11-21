package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/install"
)

func shellFor() (install.Shell, error) {
	home, _ := os.UserHomeDir()
	return install.Detect(os.Getenv("SHELL"), home)
}

// executablePath is where this safeshell lives, with symlinks resolved; tests replace it.
var executablePath = func() (string, error) {
	bin, err := os.Executable()
	if err == nil {
		bin, err = filepath.EvalSymlinks(bin)
	}
	return bin, err
}

// systemRoot is the folder the machine-wide files live under, and isRoot says whether we may write them; tests replace both.
var (
	systemRoot = "/"
	isRoot     = func() bool { return os.Geteuid() == 0 }
)

// runInstall is `safeshell install [--ai|--system]`: it adds the hook to your shell startup file, or to every user's.
func runInstall(args []string, stdout, stderr io.Writer) int {
	withAI, system := false, false
	for _, a := range args {
		switch a {
		case "--ai":
			withAI = true
		case "--system":
			system = true
		default:
			fmt.Fprint(stderr, usage)
			return 2
		}
	}
	if system {
		if withAI {
			fmt.Fprint(stderr, usage)
			return 2
		}
		return runInstallSystem(stdout, stderr)
	}
	sh, err := shellFor()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell install:", err, "\nYou can still add the hook by hand: safeshell hook bash|zsh|fish")
		return 1
	}
	bin, err := executablePath()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell install:", err)
		return 1
	}
	change, err := install.Add(sh, bin)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell install:", err)
		return 1
	}
	switch change {
	case install.Added:
		fmt.Fprintf(stdout, "Added the SafeShell hook to %s (a copy of the old file is %s.safeshell-backup).\n", sh.RC, sh.RC)
	case install.Updated:
		fmt.Fprintf(stdout, "Updated the SafeShell hook in %s.\n", sh.RC)
	default:
		fmt.Fprintf(stdout, "The SafeShell hook is already in %s.\n", sh.RC)
	}
	fmt.Fprintln(stdout, "Open a new terminal (or run:  source "+sh.RC+" ) and every command you press Enter on is checked first.")
	if r := probe(); !r.CanSimulate() {
		fmt.Fprintln(stdout, "\nThe sandbox is not available on this machine yet:")
		for _, h := range r.Hints() {
			fmt.Fprintln(stdout, "  -", h)
		}
		fmt.Fprintln(stdout, "  Fix on Ubuntu 24.04+:  scripts/install-apparmor.sh", bin)
	}
	if withAI {
		fmt.Fprintln(stdout)
		return runAI([]string{"setup"}, stdout, stderr)
	}
	fmt.Fprintln(stdout, "\nNext: safeshell ai setup   (downloads the built-in AI, optional)   |   safeshell help start")
	return 0
}

// runInstallSystem is `safeshell install --system`: the hook for every user of this machine.
func runInstallSystem(stdout, stderr io.Writer) int {
	if !isRoot() {
		fmt.Fprintln(stderr, "safeshell install --system changes files in /etc, so it must run as root. Run it again with sudo:  sudo safeshell install --system")
		return 1
	}
	bin, err := executablePath()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell install:", err)
		return 1
	}
	files, err := install.AddSystem(systemRoot, bin)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell install:", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, "The SafeShell hook is already set up for every user.")
		return 0
	}
	fmt.Fprintln(stdout, "Added the SafeShell hook for every user in:")
	for _, f := range files {
		fmt.Fprintln(stdout, "  "+f)
	}
	fmt.Fprintln(stdout, "New login and terminal sessions are checked first. Undo with:  sudo safeshell uninstall --system")
	return 0
}

// runUninstallSystem is `safeshell uninstall --system`: it takes the machine-wide hook out of /etc.
func runUninstallSystem(stdout, stderr io.Writer) int {
	if !isRoot() {
		fmt.Fprintln(stderr, "safeshell uninstall --system changes files in /etc, so it must run as root. Run it again with sudo:  sudo safeshell uninstall --system")
		return 1
	}
	files, err := install.RemoveSystem(systemRoot)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell uninstall:", err)
		return 1
	}
	if len(files) == 0 {
		fmt.Fprintln(stdout, "The SafeShell hook was not set up for every user.")
		return 0
	}
	fmt.Fprintln(stdout, "Removed the SafeShell hook for every user from:")
	for _, f := range files {
		fmt.Fprintln(stdout, "  "+f)
	}
	fmt.Fprintln(stdout, "New sessions are no longer guarded. Each user's own  safeshell install  is not touched.")
	return 0
}

// runUninstall is `safeshell uninstall [--system]`: it removes the hook and leaves everything else alone.
func runUninstall(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "--system" {
		return runUninstallSystem(stdout, stderr)
	}
	if !unexpectedArgs("uninstall", args, stderr) {
		return 2
	}
	sh, err := shellFor()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell uninstall:", err)
		return 1
	}
	changed, err := install.Remove(sh)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell uninstall:", err)
		return 1
	}
	if !changed {
		fmt.Fprintf(stdout, "The SafeShell hook was not in %s.\n", sh.RC)
		return 0
	}
	fmt.Fprintf(stdout, "Removed the SafeShell hook from %s. New terminals are no longer guarded.\n", sh.RC)
	if os.Getenv("SAFESHELL_OFF") == "" {
		fmt.Fprintln(stdout, "This terminal still has it until you close it. Your snapshots and settings were kept.")
	}
	return 0
}
