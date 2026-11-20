package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var hookShells = []struct {
	name string
	argv []string
}{{"bash", bashArgv}, {"zsh", zshArgv}}

// allHookShells adds fish, which has no here-documents and keeps a multi-line command in one buffer, so the
// tests about collecting continuation lines do not apply to it.
var allHookShells = append(hookShells[:len(hookShells):len(hookShells)], struct {
	name string
	argv []string
}{"fish", fishArgv})

// hooked starts a shell with the real hook loaded from the real binary's absolute path.
func hooked(t *testing.T, shell string, argv []string, extraEnv ...string) *term {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip(shell + " is not installed")
	}
	sh := startShell(t, argv, extraEnv...)
	sh.waitFor(t, "READY>")
	load := `eval "$(` + ssBin + ` hook ` + shell + `)"`
	if shell == "fish" {
		load = ssBin + " hook fish | source"
	}
	sh.send(load + "\r")
	return sh
}

// shows is true when the terminal has printed want since the last send.
func (tm *term) shows(want string) bool {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	return strings.Contains(tm.buf.String(), want)
}

func victimDir(t *testing.T) string {
	t.Helper()
	v := filepath.Join(t.TempDir(), "proj")
	_ = os.MkdirAll(v, 0o755)
	_ = os.WriteFile(filepath.Join(v, "a.txt"), []byte("x"), 0o644)
	return v
}

func TestHookDoesNotNeedSafeshellOnThePath(t *testing.T) {
	needSandbox(t)
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			sh := hooked(t, c.name, c.argv, "PATH=/usr/bin:/bin")
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "[c] cancel")
			sh.send("c\r")
			sh.waitFor(t, "Nothing was run")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("the dangerous command ran")
			}
		})
	}
}

func TestHookUsesItsOwnBinaryNotAnOlderOneOnThePath(t *testing.T) {
	needSandbox(t)
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			old := t.TempDir()
			_ = os.WriteFile(filepath.Join(old, "safeshell"), []byte("#!/bin/sh\nexit 7\n"), 0o755)
			sh := hooked(t, c.name, c.argv, "PATH="+old+":/usr/bin:/bin")
			sh.send("echo checked-by-the-right-one\r")
			sh.waitFor(t, "checked-by-the-right-one\r\n")
			if sh.shows("NOT checked") {
				t.Fatal("the hook called the safeshell on PATH")
			}
		})
	}
}

func TestHereDocBodyIsNeverPromptedOnOrChanged(t *testing.T) {
	needSandbox(t)
	for _, c := range hookShells {
		t.Run(c.name, func(t *testing.T) {
			sh := hooked(t, c.name, c.argv)
			sh.send("cat <<EOF\r")
			sh.send("rm -rf / in heredoc\r")
			time.Sleep(500 * time.Millisecond)
			if sh.shows("[c] cancel") || sh.shows("Proposed") {
				t.Fatal("a here-document body was checked as a command")
			}
			sh.send("EOF\r") // cat prints the body after the typed lines were echoed
			sh.waitFor(t, "rm -rf / in heredoc\r\n")
			if sh.shows("[c] cancel") {
				t.Fatal("the finished here-document was stopped")
			}
		})
	}
}

func TestMultiLineCommandIsCheckedOnceWhenComplete(t *testing.T) {
	needSandbox(t)
	for _, c := range hookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			sh := hooked(t, c.name, c.argv)
			sh.send("echo first-part &&\r")
			time.Sleep(500 * time.Millisecond)
			if sh.shows("[c] cancel") {
				t.Fatal("the unfinished command was checked")
			}
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "[c] cancel")
			sh.send("c\r")
			sh.waitFor(t, "Nothing was run")
			sh.send("echo after-cancel\r")
			sh.waitFor(t, "after-cancel\r\n")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("the dangerous multi-line command ran")
			}
			if sh.shows("first-part\r\n") {
				t.Fatal("part of the cancelled command ran")
			}
		})
	}
}

func TestSafeMultiLineCommandRuns(t *testing.T) {
	needSandbox(t)
	for _, c := range hookShells {
		t.Run(c.name, func(t *testing.T) {
			sh := hooked(t, c.name, c.argv)
			sh.send("echo one &&\r")
			sh.send("echo two\r")
			sh.waitFor(t, "two\r\n")
		})
	}
}

// After a trailing backslash an empty last line would run the first line alone,
// so a cancelled command must be dropped whole, never finished with an empty line.
func TestCancelledBackslashCommandDoesNotRunItsFirstLine(t *testing.T) {
	needSandbox(t)
	for _, c := range hookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			sh := hooked(t, c.name, c.argv)
			sh.send("rm -rf " + victim + " \\\r")
			time.Sleep(500 * time.Millisecond)
			sh.send("--verbose\r")
			sh.waitFor(t, "[c] cancel")
			sh.send("c\r")
			sh.waitFor(t, "Nothing was run")
			sh.send("echo after-cancel\r")
			sh.waitFor(t, "after-cancel\r\n")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("the first line of the cancelled command ran on its own")
			}
		})
	}
}
