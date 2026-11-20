package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/engine"
)

const (
	goodScript = "#!/bin/sh\nls\necho hi\n"
	r2Script   = "#!/bin/sh\nrm notes.txt\n"
	badScript  = "#!/bin/sh\nDIR=$1\nrm -rf \"$DIR\"/\n"
)

func writeNamed(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckScriptSeveralFilesTakesTheHighestLevel(t *testing.T) {
	d := t.TempDir()
	good, bad := writeNamed(t, d, "good.sh", goodScript), writeNamed(t, d, "bad.sh", badScript)
	code, out, _ := runSS("check", "--script", good, bad)
	if code != 3 {
		t.Errorf("exit %d, want 3", code)
	}
	for _, want := range []string{good + "\n", bad + "\n", "No risky commands found.", "    3  R3  ", "R3 Irreversible"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Count(out, "R3 Irreversible") != 1 {
		t.Errorf("one result line for all files:\n%s", out)
	}
}

func TestCheckScriptFormats(t *testing.T) {
	d := t.TempDir()
	r2, bad := writeNamed(t, d, "r2.sh", r2Script), writeNamed(t, d, "bad.sh", badScript)
	code, out, _ := runSS("check", "--script", r2, bad, "--format", "json")
	var files []struct {
		Path  string
		Level string
	}
	if err := json.Unmarshal([]byte(out), &files); err != nil || code != 3 || len(files) != 2 || files[0].Level != "R2" || files[1].Path != bad || files[1].Level != "R3" {
		t.Errorf("json: code %d err %v:\n%s", code, err, out)
	}

	code, out, _ = runSS("check", "--format", "sarif", "--script", bad)
	var log struct {
		Version string
		Runs    []struct{ Results []struct{ RuleID string } }
	}
	if err := json.Unmarshal([]byte(out), &log); err != nil || code != 3 || log.Version != "2.1.0" || log.Runs[0].Results[0].RuleID != "R3" {
		t.Errorf("sarif: code %d err %v:\n%s", code, err, out)
	}

	code, out, _ = runSS("check", "--script", r2, bad, "--format", "github")
	if !strings.HasPrefix(out, "::warning file="+r2+",line=2,title=SafeShell R2::") || !strings.Contains(out, "\n::error file="+bad+",line=3,title=SafeShell R3::") || code != 3 {
		t.Errorf("github: code %d:\n%s", code, out)
	}
}

func TestCheckScriptFailOn(t *testing.T) {
	d := t.TempDir()
	good, r2 := writeNamed(t, d, "good.sh", goodScript), writeNamed(t, d, "r2.sh", r2Script)
	r3, r4 := writeNamed(t, d, "r3.sh", badScript), writeNamed(t, d, "r4.sh", "rm -rf /\n")
	for _, tc := range []struct {
		failOn, file string
		want         int
	}{
		{"R2", r2, 1}, {"R2", r3, 3}, {"R2", good, 0},
		{"R3", r2, 0}, {"R3", r3, 3},
		{"R4", r3, 0}, {"R4", r4, 4},
	} {
		if code, _, _ := runSS("check", "--script", tc.file, "--fail-on", tc.failOn); code != tc.want {
			t.Errorf("--fail-on %s on %s: exit %d, want %d", tc.failOn, filepath.Base(tc.file), code, tc.want)
		}
	}
	if code, _, _ := runSS("check", "--script", r2); code != 0 {
		t.Errorf("default is R3: exit %d", code)
	}
}

func TestCheckScriptBadFileDoesNotHideTheOthers(t *testing.T) {
	d := t.TempDir()
	bad := writeNamed(t, d, "bad.sh", badScript)
	missing := filepath.Join(d, "none.sh")
	code, out, errOut := runSS("check", "--script", missing, bad)
	if code != 3 || !strings.Contains(errOut, "cannot read") || !strings.Contains(out, "R3 Irreversible") {
		t.Errorf("code %d:\n%s\n%s", code, out, errOut)
	}
	if code, _, _ = runSS("check", "--script", missing, writeNamed(t, d, "ok.sh", goodScript)); code != 2 {
		t.Errorf("an unreadable file and clean ones is exit 2; code %d", code)
	}
	if code, out, _ = runSS("check", "--script", missing); code != 2 || out != "" {
		t.Errorf("nothing readable, nothing printed; code %d:\n%s", code, out)
	}
}

func TestCheckScriptOptionErrors(t *testing.T) {
	f := writeNamed(t, t.TempDir(), "a.sh", goodScript)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--script", f, "--format"}, "Usage"},
		{[]string{"--script", f, "--fail-on"}, "Usage"},
		{[]string{"--script", f, "--format", "xml"}, "unknown format xml"},
		{[]string{"--script", f, "--fail-on", "R1"}, "--fail-on takes R2, R3 or R4"},
		{[]string{"--script", f, "--fail-on", "high"}, "--fail-on takes R2, R3 or R4"},
		{[]string{"--script", f, "--color"}, "unknown option --color"},
		{[]string{"--format", "json", "--script"}, "Usage"},
	} {
		code, _, errOut := runSS(append([]string{"check"}, tc.args...)...)
		if code != 2 || !strings.Contains(errOut, tc.want) {
			t.Errorf("%v: code %d, want %q in %q", tc.args, code, tc.want, errOut)
		}
	}
}

func TestCheckWithoutScriptIgnoresScriptLookingWords(t *testing.T) {
	if code, out, _ := runSS("check", "echo", "--script"); code != 0 || !strings.Contains(out, "R0") {
		t.Errorf("a command line that mentions --script after the command: code %d:\n%s", code, out)
	}
}

// The CI job for the action runs these two files: one must pass, one must fail.
func TestCheckActionFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "test", "fixtures", "check-action")
	if code, _, _ := runSS("check", "--script", filepath.Join(dir, "good.sh")); code != 0 {
		t.Errorf("good.sh: exit %d, want 0", code)
	}
	if code, _, _ := runSS("check", "--script", filepath.Join(dir, "bad.sh")); code != 3 {
		t.Errorf("bad.sh: exit %d, want 3", code)
	}
}

// The script check is the rules only: it must not reach the AI, the engine or the sandbox.
func TestCheckScriptUsesNoSandboxNoAINoDaemon(t *testing.T) {
	oldBackend, oldAnalyze := backend, analyze
	t.Cleanup(func() { backend, analyze = oldBackend, oldAnalyze })
	backend = func() (ai.Backend, error) { t.Error("check --script started the AI"); return nil, nil }
	analyze = func(context.Context, string, string) engine.Analysis {
		t.Error("check --script ran the engine")
		return engine.Analysis{}
	}
	d := t.TempDir()
	for _, f := range []string{"text", "json", "sarif", "github"} {
		runSS("check", "--script", writeNamed(t, d, "a.sh", badScript), "--format", f)
	}
}
