package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// countIn is how many lines of the file at f contain want; a missing file has none.
func countIn(f, want string) int {
	b, _ := os.ReadFile(f)
	return strings.Count(string(b), want)
}

// settle waits until f has at least n lines containing want, then a moment longer so a duplicate would show, and returns the count.
func settle(f, want string, n int) int {
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && countIn(f, want) < n; time.Sleep(50 * time.Millisecond) {
	}
	time.Sleep(500 * time.Millisecond)
	return countIn(f, want)
}

func TestHooksLogChecksTurnedOffOncePerTerminalUntilTheyAreTurnedOnAgain(t *testing.T) {
	needSandbox(t)
	for _, tc := range []struct {
		shell   string
		argv    []string
		off, on string
	}{
		{"bash", bashArgv, "export SAFESHELL_OFF=1", "unset SAFESHELL_OFF"},
		{"zsh", zshArgv, "export SAFESHELL_OFF=1", "unset SAFESHELL_OFF"},
		{"fish", fishArgv, "set -x SAFESHELL_OFF 1", "set -e SAFESHELL_OFF"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			state := t.TempDir()
			log := filepath.Join(state, "safeshell", "audit.jsonl")
			sh := hooked(t, tc.shell, tc.argv, "XDG_STATE_HOME="+state)
			run := func(line, marker string) {
				sh.send(line + "\r")
				if marker != "" {
					sh.waitFor(t, marker+"\r\n")
				}
				time.Sleep(200 * time.Millisecond)
			}
			run("echo before", "before")
			run(tc.off, "")
			run("echo one", "one")
			run("echo two", "two")
			if n := settle(log, `"outcome":"checks-off"`, 1); n != 1 {
				t.Fatalf("%d events after two unchecked lines, want 1", n)
			}
			run(tc.on, "")
			run("echo three", "three")
			run(tc.off, "")
			run("echo four", "four")
			if n := settle(log, `"outcome":"checks-off"`, 2); n != 2 {
				t.Errorf("%d events, want 2 (one each time checks were turned off)", n)
			}
		})
	}
}

func TestHooksLogWhenSafeShellFailsAndALineRunsUnchecked(t *testing.T) {
	needSandbox(t)
	for _, tc := range []struct {
		shell string
		argv  []string
		swap  string // points the hook at the fake binary
	}{
		{"bash", bashArgv, "__ss_bin=@FAKE@"},
		{"zsh", zshArgv, "__ss_bin=@FAKE@"},
		{"fish", fishArgv, "function __ss_bin; @FAKE@ $argv; end"},
	} {
		t.Run(tc.shell, func(t *testing.T) {
			d := t.TempDir()
			seen := filepath.Join(d, "seen")
			fake := filepath.Join(d, "fake")
			// Answers every guard call with exit status 7 (a crash) and records every note.
			script := "#!/bin/sh\nif [ \"$1\" = note ]; then echo \"$@\" >> " + seen + "; exit 0; fi\nexit 7\n"
			if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			sh := hooked(t, tc.shell, tc.argv)
			sh.send(strings.ReplaceAll(tc.swap, "@FAKE@", fake) + "\r")
			if tc.shell == "fish" { // the guard cannot read a function definition, so it asks first
				sh.waitFor(t, "Run it anyway?")
				sh.send("y\r")
			}
			sh.send("echo ran-anyway\r")
			sh.waitFor(t, "NOT checked")
			if n := settle(seen, "note not-checked 7\n", 1); n != 1 {
				b, _ := os.ReadFile(seen)
				t.Errorf("%d notes, want 1:\n%s", n, b)
			}
		})
	}
}
