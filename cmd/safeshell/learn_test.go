package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// learnConfig points the settings at an empty folder and restores the real
// learning check, which TestMain replaces.
func learnConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := learning
	t.Cleanup(func() { learning = old })
	learning = readLearning
}

func TestLearnOnOffStatus(t *testing.T) {
	learnConfig(t)
	if code, out, _ := runSS("learn"); code != 0 || !strings.Contains(out, "off. Turn it on: safeshell learn on") {
		t.Errorf("default: %d %q", code, out)
	}
	if code, out, _ := runSS("learn", "on"); code != 0 || !strings.Contains(out, "Learning mode is on") || !strings.Contains(out, "safeshell learn off") {
		t.Errorf("on: %d %q", code, out)
	}
	if code, out, _ := runSS("learn", "status"); code != 0 || !strings.Contains(out, "is on") {
		t.Errorf("status: %d %q", code, out)
	}
	if c, _ := config.Load(config.Path()); !c.Learn {
		t.Error("on must be saved in the config")
	}
	if code, out, _ := runSS("learn", "off"); code != 0 || !strings.Contains(out, "is off") {
		t.Errorf("off: %d %q", code, out)
	}
}

func TestLearnKeepsOtherSettings(t *testing.T) {
	learnConfig(t)
	c, _ := config.Load(config.Path())
	c.AI.Cloud.Provider = "gemini"
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	runSS("learn", "on")
	if c, _ := config.Load(config.Path()); c.AI.Cloud.Provider != "gemini" || !c.Learn {
		t.Errorf("%+v", c)
	}
}

func TestLearnErrors(t *testing.T) {
	learnConfig(t)
	for _, args := range [][]string{{"maybe"}, {"on", "off"}} {
		if code, _, e := runSS(append([]string{"learn"}, args...)...); code != 2 || !strings.Contains(e, "Usage") {
			t.Errorf("%v: %d %q", args, code, e)
		}
	}
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, e := runSS("learn", "on"); code != 1 || !strings.Contains(e, "safeshell learn:") {
		t.Errorf("broken settings: %d %q", code, e)
	}
	if learning() {
		t.Error("unreadable settings count as off")
	}
	if err := os.Remove(config.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(config.Path()), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Dir(config.Path()), 0o700) })
	if os.Getuid() != 0 {
		if code, _, e := runSS("learn", "on"); code != 1 || !strings.Contains(e, "cannot save the setting") {
			t.Errorf("read-only settings: %d %q", code, e)
		}
	}
}

// guardWith runs the hook's guard on line with the given analysis and returns the exit code, stdout and stderr.
func guardWith(t *testing.T, line string, level domain.Level, d domain.Decision) (int, string, string) {
	t.Helper()
	a := sampleAnalysis()
	a.Line, a.Level, a.Decision = line, level, d
	a.Verdict.Level = level
	a.Verdict.Findings = []domain.Finding{domain.Finding{Level: level, Command: line, Reason: "Copies files."}.WithAlt("cp -n a b", "does not overwrite")}
	a.Verdict.Alternatives = nil
	fakeAnalyze(t, a)
	answer(t, "")
	return runSS("guard", "--", line)
}

func TestGuardLearningModeAddsOneLineAndChangesNothingElse(t *testing.T) {
	learnConfig(t)
	offCode, offOut, offErr := guardWith(t, "cp a b", domain.R1, domain.Allow)
	if strings.Contains(offErr, "Learn:") {
		t.Fatalf("learning is off by default: %q", offErr)
	}
	runSS("learn", "on")
	code, out, e := guardWith(t, "cp a b", domain.R1, domain.Allow)
	want := "Learn: R1 Reversible. Copies files. Safer: cp -n a b (does not overwrite)\n"
	if e != offErr+want {
		t.Errorf("stderr: %q", e)
	}
	if code != offCode || out != offOut {
		t.Errorf("decision changed: %d %q vs %d %q", code, out, offCode, offOut)
	}
}

func TestGuardLearningModeSkipsReadsAndFullReports(t *testing.T) {
	learnConfig(t)
	runSS("learn", "on")
	if _, _, e := guardWith(t, "ls", domain.R0, domain.Allow); e != "" {
		t.Errorf("an R0 read gets no line: %q", e)
	}
	_, _, e := guardWith(t, "rm -r build", domain.R3, domain.Block)
	if strings.Contains(e, "Learn:") || !strings.Contains(e, "R3") {
		t.Errorf("a blocked command is already explained in full: %q", e)
	}
}

func TestLearnNoteColour(t *testing.T) {
	learnConfig(t)
	runSS("learn", "on")
	a := sampleAnalysis()
	a.Verdict.Level = domain.R1
	var b strings.Builder
	learnNote(&b, a, true)
	if !strings.HasPrefix(b.String(), "\033[2mLearn: R1") || !strings.HasSuffix(b.String(), "\033[0m\n") {
		t.Errorf("%q", b.String())
	}
}

// Turning learning mode on changes only the "learn" key, so today's defaults are not frozen into the file.
func TestLearnOnlyTouchesItsOwnKey(t *testing.T) {
	learnConfig(t)
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(`{"x":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runSS("learn", "on")
	b, _ := os.ReadFile(config.Path())
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil || len(got) != 2 || got["x"] != 1.0 || got["learn"] != true {
		t.Errorf("%s %v", b, err)
	}
}
