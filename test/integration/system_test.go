package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/install"
)

// These tests cannot be root, so they write the machine-wide files into a temp
// root with the real code and have a real interactive shell read them, the way
// the shell would read /etc. Each shell is checked for the hook and for the
// absence of a second copy.

// hookPid matches the printed "[hook pid/shell pid]"; both numbers are equal when the hook is loaded in that shell.
var hookPid = regexp.MustCompile(`\[(\d+)/(\d+)\]`)

// systemRootWith installs the system hook under a fresh root where every shell has its folder.
func systemRootWith(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"etc/zsh", "etc/fish"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc/bash.bashrc"), []byte("# distro file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := install.AddSystem(root, ssBin); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestSystemHookIsLoadedByAFreshUsersShell(t *testing.T) {
	if _, err := os.Stat(ssBin); err != nil {
		t.Skip("bin/safeshell not built (run: make build)")
	}
	root := systemRootWith(t)
	cases := []struct {
		name string
		argv []string
		file string
		echo string // prints the hook's pid marker and this shell's own pid
	}{
		{"bash login file", []string{"bash", "--norc", "--noprofile", "-i"}, "etc/profile.d/safeshell.sh", `echo "[$SAFESHELL_HOOK/$$]"`},
		{"bash.bashrc", []string{"bash", "--norc", "--noprofile", "-i"}, "etc/bash.bashrc", `echo "[$SAFESHELL_HOOK/$$]"`},
		{"zsh", zshArgv, "etc/zsh/zshrc", `echo "[$SAFESHELL_HOOK/$$]"`},
		{"fish", fishArgv, "etc/fish/conf.d/safeshell.fish", `echo "[$SAFESHELL_HOOK/$fish_pid]"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := exec.LookPath(c.argv[0]); err != nil {
				t.Skip(c.argv[0] + " is not installed")
			}
			home := t.TempDir() // a new user: no startup files of their own
			sh := startShell(t, c.argv, "HOME="+home)
			sh.waitFor(t, "READY>")
			sh.send("source " + filepath.Join(root, c.file) + "\r")
			sh.waitFor(t, "READY>")
			sh.send(c.echo + "\r")
			sh.waitFor(t, "READY>") // the prompt after the output
			sh.mu.Lock()
			out := sh.buf.String()
			sh.mu.Unlock()
			if m := hookPid.FindStringSubmatch(out); m == nil || m[1] != m[2] {
				t.Errorf("the hook was not loaded in this shell: %q", out)
			}
		})
	}
}

func TestSystemHookIsNotLoadedTwice(t *testing.T) {
	if _, err := os.Stat(ssBin); err != nil {
		t.Skip("bin/safeshell not built (run: make build)")
	}
	root := systemRootWith(t)
	home := t.TempDir()
	sh := startShell(t, []string{"bash", "--norc", "--noprofile", "-i"}, "HOME="+home)
	sh.waitFor(t, "READY>")
	// A login shell reads profile.d and then bash.bashrc; the second must see the first's hook.
	sh.send("source " + filepath.Join(root, "etc/profile.d/safeshell.sh") + "; source " + filepath.Join(root, "etc/bash.bashrc") + "\r")
	sh.waitFor(t, "READY>")
	sh.send(`echo "n=${PROMPT_COMMAND//[^_]/}"` + "\r")
	sh.waitFor(t, "n=___\r\n") // "__ss_reset" has three underscores; two copies would show six
}

func TestSystemHookStillGuardsACommand(t *testing.T) {
	needSandbox(t)
	root := systemRootWith(t)
	victim := victimDir(t)
	sh := startShell(t, []string{"bash", "--norc", "--noprofile", "-i"}, "HOME="+t.TempDir())
	sh.waitFor(t, "READY>")
	sh.send("source " + filepath.Join(root, "etc/profile.d/safeshell.sh") + "\r")
	sh.waitFor(t, "READY>")
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the dangerous command ran")
	}
}

func TestSystemHookDoesNothingWithoutAnInteractiveShell(t *testing.T) {
	if _, err := os.Stat(ssBin); err != nil {
		t.Skip("bin/safeshell not built (run: make build)")
	}
	root := systemRootWith(t)
	out, err := exec.Command("bash", "--norc", "--noprofile", "-c", `. "$1"; echo "[${SAFESHELL_HOOK-none}]"`, "x", filepath.Join(root, "etc/profile.d/safeshell.sh")).Output()
	if err != nil || strings.TrimSpace(string(out)) != "[none]" {
		t.Errorf("a script must not get the hook: %q %v", out, err)
	}
}
