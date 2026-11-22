package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// autorunConfig points the settings at an empty folder and restores the real
// autorun check, which TestMain replaces.
func autorunConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := autorunOn
	t.Cleanup(func() { autorunOn = old })
	autorunOn = readAutorun
}

func TestAutorunOnOffStatus(t *testing.T) {
	autorunConfig(t)
	if code, out, _ := runSS("autorun"); code != 0 || !strings.Contains(out, "Autorun is off") || !strings.Contains(out, "safeshell autorun on") {
		t.Errorf("default: %d %q", code, out)
	}
	if code, out, _ := runSS("autorun", "on"); code != 0 || !strings.Contains(out, "Autorun is on") || !strings.Contains(out, "safeshell autorun off") {
		t.Errorf("on: %d %q", code, out)
	}
	if code, out, _ := runSS("autorun", "status"); code != 0 || !strings.Contains(out, "is on") {
		t.Errorf("status: %d %q", code, out)
	}
	if c, _ := config.Load(config.Path()); !c.Autorun {
		t.Error("on must be saved in the config")
	}
	if !autorunOn() {
		t.Error("the saved setting must be read back")
	}
	if code, out, _ := runSS("autorun", "off"); code != 0 || !strings.Contains(out, "is off") {
		t.Errorf("off: %d %q", code, out)
	}
	if autorunOn() {
		t.Error("off must be read back as off")
	}
}

func TestAutorunKeepsOtherSettings(t *testing.T) {
	autorunConfig(t)
	c, _ := config.Load(config.Path())
	c.AI.Cloud.Provider = "gemini"
	c.Learn = true
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	runSS("autorun", "on")
	if c, _ := config.Load(config.Path()); c.AI.Cloud.Provider != "gemini" || !c.Learn || !c.Autorun {
		t.Errorf("%+v", c)
	}
}

func TestAutorunErrors(t *testing.T) {
	autorunConfig(t)
	for _, args := range [][]string{{"maybe"}, {"on", "off"}} {
		if code, _, e := runSS(append([]string{"autorun"}, args...)...); code != 2 || !strings.Contains(e, "Usage") {
			t.Errorf("%v: %d %q", args, code, e)
		}
	}
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runSS("autorun", "on"); code != 1 || !strings.Contains(e, "safeshell autorun:") {
		t.Errorf("broken settings: %d %q", code, e)
	}
	if autorunOn() {
		t.Error("unreadable settings count as off")
	}
	if err := os.Remove(config.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(config.Path()), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Dir(config.Path()), 0o700) })
	if os.Geteuid() != 0 {
		if code, _, e := runSS("autorun", "on"); code != 1 || !strings.Contains(e, "cannot save the setting") {
			t.Errorf("unwritable settings: %d %q", code, e)
		}
	}
}

func TestAutorunIsOffInStrictMode(t *testing.T) {
	autorunConfig(t)
	runSS("autorun", "on")
	oldTeam := teamFile
	t.Cleanup(func() { teamFile = oldTeam })

	teamFile = func() (policy.File, bool, error) { return policy.File{}, false, nil }
	if !autorunOn() {
		t.Fatal("autorun should be on")
	}
	teamFile = func() (policy.File, bool, error) { return policy.File{Strict: true}, true, nil }
	if autorunOn() {
		t.Error("the team's strict mode turns autorun off")
	}
	teamFile = func() (policy.File, bool, error) { return policy.File{}, true, errors.New("bad policy") }
	if autorunOn() {
		t.Error("a policy that cannot be read turns autorun off")
	}
	teamFile = func() (policy.File, bool, error) { return policy.File{}, false, nil }
	c, _ := config.Load(config.Path())
	c.Strict = true
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	if autorunOn() {
		t.Error("your own strict mode turns autorun off")
	}
}

// askWith runs "safeshell ask" with the AI proposing cmd, the checks rating it
// as a, and no answer ready to read, so a question shows up as a cancelled run.
func askWith(t *testing.T, cmd string, mutate func(*testing.T)) (code int, out, errOut string) {
	t.Helper()
	useAI(t, fakeAI{reply: `{"command":"` + cmd + `","note":"does it"}`}, nil)
	answer(t, "")
	if mutate != nil {
		mutate(t)
	}
	return runSS("ask", "do", "the", "thing")
}

func autorunSet(t *testing.T, on bool) {
	t.Helper()
	old := autorunOn
	t.Cleanup(func() { autorunOn = old })
	autorunOn = func() bool { return on }
}

func TestWithoutAutorunASafeCommandStillAsks(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R1, domain.Allow
	fakeAnalyze(t, a)
	autorunSet(t, false)
	code, out, errOut := askWith(t, "mv notes.txt backup/", nil)
	if code != 1 || out != "" || !strings.Contains(errOut, "Run it?") || strings.Contains(errOut, "Running it now") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestAutorunRunsASafeCommandWithoutAsking(t *testing.T) {
	for _, l := range []domain.Level{domain.R0, domain.R1} {
		a := sampleAnalysis()
		a.Level, a.Decision = l, domain.Allow
		fakeAnalyze(t, a)
		autorunSet(t, true)
		code, out, errOut := askWith(t, "mv notes.txt backup/", nil)
		if code != 0 || out != "mv notes.txt backup/\n" || !strings.Contains(errOut, "Proposed: mv notes.txt backup/") ||
			!strings.Contains(errOut, "Running it now") || !strings.Contains(errOut, "safeshell autorun off") || strings.Contains(errOut, "Run it?") {
			t.Errorf("%s: %d %q %q", l, code, out, errOut)
		}
		if e := lastAudit(t); e.Outcome != "allowed" {
			t.Errorf("%s: an autorun command is still recorded as allowed: %+v", l, e)
		}
	}
}

func TestAutorunLetsCdThrough(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	autorunSet(t, true)
	if code, out, _ := askWith(t, "cd projects", nil); code != 0 || out != "cd projects\n" {
		t.Errorf("%d %q", code, out)
	}
}

func TestAutorunNeverSkipsARiskierQuestion(t *testing.T) {
	for _, c := range []struct {
		name   string
		cmd    string
		mutate func(*engine.Analysis)
	}{
		{"R2 asks", "chmod -R 755 projects", func(a *engine.Analysis) { a.Level, a.Decision = domain.R2, domain.Confirm }},
		{"R3 is blocked", "rm -rf projects", func(a *engine.Analysis) { a.Level, a.Decision = domain.R3, domain.Block }},
		{"R4 is stopped", "rm -rf /", func(a *engine.Analysis) { a.Level, a.Decision = domain.R4, domain.HardBlock }},
		{"a program the rules do not know", "mytool --go", func(a *engine.Analysis) { a.Verdict.Unclassified = []string{"mytool"} }},
		{"a broken team policy", "ls", func(a *engine.Analysis) { a.PolicyWarning = "the team policy could not be read" }},
		{"a command that changes the shell", "export FOO=1", nil},
		{"a command that sets a variable", "FOO=1", nil},
	} {
		a := sampleAnalysis()
		a.Level, a.Decision = domain.R1, domain.Allow
		if c.mutate != nil {
			c.mutate(&a)
		}
		fakeAnalyze(t, a)
		autorunSet(t, true)
		code, out, errOut := askWith(t, c.cmd, nil)
		if code != 1 || out != "" || strings.Contains(errOut, "Running it now") {
			t.Errorf("%s: %d %q %q", c.name, code, out, errOut)
		}
	}
}
