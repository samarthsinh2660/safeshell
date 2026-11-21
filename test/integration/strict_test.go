package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// strictConfig is a settings folder with strict mode on, for XDG_CONFIG_HOME.
func strictConfig(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.MkdirAll(filepath.Join(d, "safeshell"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "safeshell", "config.json"), []byte(`{"strict": true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestStrictModeCancelsRiskyLinesWhenSafeShellBreaksButReadOnlyOnesRun(t *testing.T) {
	needSandbox(t)
	b, err := os.ReadFile(ssBin)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			cp := filepath.Join(t.TempDir(), "safeshell")
			if err := os.WriteFile(cp, b, 0o755); err != nil {
				t.Fatal(err)
			}
			sh := startShell(t, c.argv, "XDG_CONFIG_HOME="+strictConfig(t))
			sh.waitFor(t, "READY>")
			load := `eval "$(` + cp + ` hook ` + c.name + `)"`
			if c.name == "fish" {
				load = cp + " hook fish | source"
			}
			sh.send(load + "\r")
			sh.send("echo hooked\r")
			sh.waitFor(t, "hooked\r\n")
			// Break SafeShell: the same file now fails with exit 7.
			if err := os.WriteFile(cp, []byte("#!/bin/sh\nexit 7\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "strict mode is on")
			sh.send("ls " + victim + "\r")
			sh.waitFor(t, "a.txt\r\n") // the read-only line ran, and so did not stop the shell
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("strict mode let the dangerous command run")
			}
			sh.send("ls " + victim + "; rm -rf " + victim + "\r")
			sh.waitFor(t, "strict mode is on")
			sh.send("echo done-check\r") // sent after the line above has been dealt with
			sh.waitFor(t, "done-check")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("strict mode let a chained dangerous command run")
			}
		})
	}
}

func TestStrictModeRefusesSafeshellOff(t *testing.T) {
	needSandbox(t)
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			sh := hooked(t, c.name, c.argv, "XDG_CONFIG_HOME="+strictConfig(t), "SAFESHELL_OFF=1")
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "SAFESHELL_OFF is ignored")
			sh.waitFor(t, "[c] cancel")
			sh.send("c\r")
			sh.waitFor(t, "Nothing was run")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("SAFESHELL_OFF skipped the check in strict mode")
			}
		})
	}
}

func TestWithoutStrictModeSafeshellOffStillPausesChecking(t *testing.T) {
	needSandbox(t)
	victim := victimDir(t)
	sh := hooked(t, "bash", bashArgv, "SAFESHELL_OFF=1")
	sh.send("rm -rf " + victim + "\r")
	sh.send("echo after-off\r")
	sh.waitFor(t, "after-off\r\n")
	if _, err := os.Stat(victim); err == nil {
		t.Fatal("the line was checked although SAFESHELL_OFF is set and strict mode is off")
	}
}

func TestStrictModeIgnoresThePauseFile(t *testing.T) {
	needSandbox(t)
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			victim := victimDir(t)
			state := t.TempDir()
			if err := os.MkdirAll(filepath.Join(state, "safeshell"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(state, "safeshell", "off"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			sh := hooked(t, c.name, c.argv, "XDG_CONFIG_HOME="+strictConfig(t), "XDG_STATE_HOME="+state)
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "Checking is not paused")
			sh.waitFor(t, "[c] cancel")
			sh.send("c\r")
			sh.waitFor(t, "Nothing was run")
			if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
				t.Fatal("the pause file skipped the check in strict mode")
			}
		})
	}
}
