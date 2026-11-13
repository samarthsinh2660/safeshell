package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func fakeAnalyze(t *testing.T, a engine.Analysis) *[2]string {
	t.Helper()
	old := analyze
	t.Cleanup(func() { analyze = old })
	var got [2]string
	analyze = func(_ context.Context, line, cwd string) engine.Analysis {
		got = [2]string{line, cwd}
		return a
	}
	return &got
}

func sampleAnalysis() engine.Analysis {
	return engine.Analysis{
		Line: "rm -rf ~/projects",
		Verdict: domain.Verdict{
			Level:        domain.R3,
			Findings:     []domain.Finding{{Level: domain.R3, Reason: "Recursively and permanently deletes whole folders."}},
			Alternatives: []domain.Alternative{{Command: "trash-put ~/projects", Note: "moves them to the Trash"}},
		},
		Effects: []domain.Effect{
			{Kind: domain.EffectFS, Evidence: domain.Observed, Summary: "would delete 4,312 files (2.0 GiB)", Details: []string{"deleted /home/me/projects/ (4,312 files)"}, MinLevel: domain.R3},
		},
		Level: domain.R3, Decision: domain.Block,
	}
}

func TestAnalyzeReport(t *testing.T) {
	got := fakeAnalyze(t, sampleAnalysis())
	code, out, _ := runSS("analyze", "--cwd", "/work", "rm", "-rf", "~/projects")
	if code != 3 {
		t.Errorf("a blocked command exits 3, got %d", code)
	}
	if got[0] != "rm -rf ~/projects" || got[1] != "/work" {
		t.Errorf("engine called with %v", got)
	}
	for _, want := range []string{
		"R3 Irreversible  BLOCK (you can override)", "Why", "OBSERVED  files: would delete 4,312 files",
		"deleted /home/me/projects/ (4,312 files)", "Safer options", "trash-put ~/projects",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyzeSaysWhenEffectsRaisedTheLevel(t *testing.T) {
	a := sampleAnalysis()
	a.Verdict.Level, a.Level, a.Decision = domain.R2, domain.R4, domain.HardBlock
	fakeAnalyze(t, a)
	code, out, _ := runSS("analyze", "x")
	if code != 4 || !strings.Contains(out, "looked like R2; what it would really do raised it to R4") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestAnalyzeAllowAndConfirmExitZero(t *testing.T) {
	for _, d := range []domain.Decision{domain.Allow, domain.Confirm} {
		a := engine.Analysis{Line: "ls", Level: domain.R0, Decision: d}
		fakeAnalyze(t, a)
		if code, _, _ := runSS("analyze", "ls"); code != 0 {
			t.Errorf("%s: exit %d", d, code)
		}
	}
}

func TestAnalyzeNotesAndJSON(t *testing.T) {
	a := sampleAnalysis()
	a.SimNote = "no sandbox available on this machine"
	a.Sim = &sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "/boot is partial"}}}
	fakeAnalyze(t, a)
	_, out, _ := runSS("analyze", "--cwd", "/boot", "x")
	if !strings.Contains(out, "no sandbox available") || !strings.Contains(out, "Sandbox note: /boot is partial") {
		t.Errorf("notes missing:\n%s", out)
	}
	_, out, _ = runSS("analyze", "--cwd", "/tmp", "x")
	if strings.Contains(out, "/boot is partial") || !strings.Contains(out, "1 sandbox note about other folders was hidden; add --verbose to see it.") {
		t.Errorf("unrelated note should be hidden with a hint:\n%s", out)
	}
	_, out, _ = runSS("analyze", "--verbose", "--cwd", "/tmp", "x")
	if !strings.Contains(out, "Sandbox note: /boot is partial") || strings.Contains(out, "were hidden") {
		t.Errorf("--verbose should show everything:\n%s", out)
	}
	code, out, _ := runSS("analyze", "--json", "x")
	var j struct {
		Level    string
		Decision string
		Effects  []struct{ Evidence, Kind string }
	}
	if code != 3 || json.Unmarshal([]byte(out), &j) != nil || j.Level != "R3" || j.Decision != "block" || j.Effects[0].Evidence != "OBSERVED" {
		t.Errorf("json: %d %s", code, out)
	}
}

func TestAnalyzeUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"analyze"}, {"analyze", "--cwd"}, {"analyze", "--bogus", "x"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestSandboxNotesFollowSymlinks(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "real")
	link := filepath.Join(d, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	ws := []sandbox.Warning{{Paths: []string{real}, Text: "real"}, {Paths: []string{"/boot"}, Text: "boot"}}
	shown, hidden := sandboxNotes(ws, "rm -rf "+link, "/work", nil, false)
	if len(shown) != 1 || shown[0].Text != "real" || hidden != 1 {
		t.Errorf("through a symlink: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf "+link, "/work", nil, true)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("verbose: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf $X", "/work", nil, false)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("uncertain: shown %v, hidden %d", shown, hidden)
	}
}

func TestHiddenNotesWording(t *testing.T) {
	for n, want := range map[int]string{
		0: "",
		1: "1 sandbox note about other folders was hidden; add --verbose to see it.\n",
		3: "3 sandbox notes about other folders were hidden; add --verbose to see them.\n",
	} {
		var b bytes.Buffer
		hiddenNotes(&b, n, "", "")
		if b.String() != want {
			t.Errorf("%d: got %q", n, b.String())
		}
	}
}

func TestSimulateVerboseShowsEveryNote(t *testing.T) {
	fakeSim(t, sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "boot note"}}}, nil)
	_, out, _ := runSS("simulate", "--cwd", "/work", "rm", "/work/x")
	if strings.Contains(out, "boot note") || !strings.Contains(out, "1 sandbox note about other folders was hidden") {
		t.Errorf("filtered:\n%s", out)
	}
	_, out, _ = runSS("simulate", "--verbose", "--cwd", "/work", "rm", "/work/x")
	if !strings.Contains(out, "Warning: boot note") || strings.Contains(out, "hidden") {
		t.Errorf("verbose:\n%s", out)
	}
}
