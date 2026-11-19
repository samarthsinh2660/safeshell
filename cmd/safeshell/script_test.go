package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/policy"
)

func writeScript(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "deploy.sh")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCheckScriptShowsTheLineAndTheHighestLevel(t *testing.T) {
	p := writeScript(t, "#!/bin/bash\n# clean up\nDIR=/tmp/x\nls -la\nrm -rf \"$DIR\"/\necho done\n")
	code, out, _ := runSS("check", "--script", p)
	if code != 3 {
		t.Errorf("a script with an R3 line exits 3, got %d", code)
	}
	for _, want := range []string{"    5  R3  ", `rm -rf "$DIR"/`, "R3 Irreversible  BLOCK (you can override)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ls -la") {
		t.Errorf("a line rated R0 is not listed:\n%s", out)
	}
}

func TestCheckScriptWithNothingRiskyIsAllowed(t *testing.T) {
	code, out, _ := runSS("check", "--script", writeScript(t, "# nothing\nls\n\npwd\n"))
	if code != 0 || !strings.Contains(out, "No risky commands found.") {
		t.Errorf("code %d:\n%s", code, out)
	}
}

func TestCheckScriptSeveralReasonsOnOneLine(t *testing.T) {
	_, out, _ := runSS("check", "--script", writeScript(t, "sudo rm -rf /tmp/x > /etc/hosts\n"))
	if strings.Count(out, "\n") < 4 || !strings.Contains(out, "    1  R") {
		t.Errorf("each reason gets a row, the number only on the first:\n%s", out)
	}
}

func TestCheckScriptErrors(t *testing.T) {
	bad := writeScript(t, "echo ok\nif true; then\n  ls\n")
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"check", "--script"}, "Usage"},
		{[]string{"check", "--script", "a", "b"}, "Usage"},
		{[]string{"check", "--script", filepath.Join(t.TempDir(), "none.sh")}, "cannot read"},
		{[]string{"check", "--script", bad}, "is not valid shell, line "},
		{[]string{"check", "--json", "--script", bad}, "--script"},
	} {
		code, out, errOut := runSS(tc.args...)
		if code != 2 || !strings.Contains(out+errOut, tc.want) {
			t.Errorf("%v: code %d, want %q in %q %q", tc.args, code, tc.want, out, errOut)
		}
	}
}

func TestAnalyzeScriptTakesTheHigherOfLinesAndObserved(t *testing.T) {
	a := engine.Analysis{
		Effects: []domain.Effect{{Kind: domain.EffectFS, Evidence: domain.Observed, Summary: "would delete 9 files", MinLevel: domain.R3}},
		Level:   domain.R3, Decision: domain.Block,
	}
	got := fakeAnalyze(t, a)
	body := "echo start\nmkdir -p out\n"
	code, out, _ := runSS("analyze", "--cwd", "/work", "--script", writeScript(t, body))
	if code != 3 || got[0] != body || got[1] != "/work" {
		t.Errorf("code %d, engine called with %q", code, got)
	}
	for _, want := range []string{"    2  R1", "mkdir -p out", "would delete 9 files", "R3 Irreversible  BLOCK (you can override)", "raised it to R3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestAnalyzeScriptTeamBlockAndNoRaise(t *testing.T) {
	fakeAnalyze(t, engine.Analysis{Level: domain.R3, Decision: domain.Block, Team: policy.Outcome{Block: "Ask the platform team first."}})
	code, out, _ := runSS("analyze", "--script", writeScript(t, "rm -rf \"$D\"/\n"))
	if code != 3 || !strings.Contains(out, "Blocked by your team's policy: Ask the platform team first.") || strings.Contains(out, "raised it") {
		t.Errorf("code %d:\n%s", code, out)
	}
}

func TestAnalyzeScriptErrors(t *testing.T) {
	fakeAnalyze(t, engine.Analysis{})
	for _, args := range [][]string{
		{"analyze", "--script"},
		{"analyze", "--script", "x.sh", "ls"},
		{"analyze", "--json", "--script", "x.sh"},
		{"analyze", "--script", filepath.Join(t.TempDir(), "none.sh")},
		{"simulate", "--script", "x.sh"},
	} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: want exit 2, got %d", args, code)
		}
	}
}

func TestScriptHelpWithValueNamedLikeAnOption(t *testing.T) {
	if wantsHelp("check", []string{"--script", "-h"}) {
		t.Error("the value of --script is not an option")
	}
}
