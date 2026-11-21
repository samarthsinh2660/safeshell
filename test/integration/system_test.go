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

// A fake safeshell whose hook only records that it was loaded and marks the shell as hooked,
// so the test can count loads in every shell without the sandbox.
func countingBinary(t *testing.T, counter string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "safeshell")
	script := "#!/bin/sh\ncase \"$2\" in\nfish) echo 'echo x >> " + counter + "; set -gx SAFESHELL_HOOK $fish_pid' ;;\n*) echo 'echo x >> " + counter + "; export SAFESHELL_HOOK=$$' ;;\nesac\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestSystemHookAndAUsersOwnHookLoadOnce(t *testing.T) {
	cases := []struct {
		name, sysFile string
		argv          []string
		user          install.Shell
	}{
		{"bash", "etc/profile.d/safeshell.sh", []string{"bash", "--norc", "--noprofile", "-i"}, install.Shell{Name: "bash", RC: ".bashrc"}},
		{"zsh", "etc/zsh/zshrc", zshArgv, install.Shell{Name: "zsh", RC: ".zshrc"}},
		{"fish", "etc/fish/conf.d/safeshell.fish", fishArgv, install.Shell{Name: "fish", RC: ".config/fish/conf.d/safeshell.fish"}},
	}
	for _, c := range cases {
		for _, systemFirst := range []bool{true, false} {
			name := c.name + " user file first"
			if systemFirst {
				name = c.name + " system file first"
			}
			t.Run(name, func(t *testing.T) {
				if _, err := exec.LookPath(c.argv[0]); err != nil {
					t.Skip(c.argv[0] + " is not installed")
				}
				counter := filepath.Join(t.TempDir(), "loads")
				bin := countingBinary(t, counter)
				root := t.TempDir()
				for _, d := range []string{"etc/zsh", "etc/fish"} {
					if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := install.AddSystem(root, bin); err != nil {
					t.Fatal(err)
				}
				home := t.TempDir()
				user := install.Shell{Name: c.user.Name, RC: filepath.Join(home, c.user.RC)}
				if _, err := install.Add(user, bin); err != nil {
					t.Fatal(err)
				}
				files := []string{filepath.Join(root, c.sysFile), user.RC}
				if !systemFirst {
					files[0], files[1] = files[1], files[0]
				}
				sh := startShell(t, c.argv, "HOME="+home)
				sh.waitFor(t, "READY>")
				for _, f := range files {
					sh.send("source " + f + "\r")
					sh.waitFor(t, "READY>")
				}
				b, err := os.ReadFile(counter)
				if err != nil || string(b) != "x\n" {
					t.Errorf("the hook must load exactly once, got %q (%v)", b, err)
				}
			})
		}
	}
}
