package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func runSS(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = run(args, &o, &e, false)
	return code, o.String(), e.String()
}

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want int
	}{
		{"ls -la", 0},
		{"cp a b", 0},
		{"rm file", 0}, // R2 asks, it does not block
		{"rm -rf build", 3},
		{"rm -rf /", 4},
	} {
		if code, _, _ := runSS("check", tc.cmd); code != tc.want {
			t.Errorf("check %q exit = %d, want %d", tc.cmd, code, tc.want)
		}
	}
}

func TestMultipleArgumentsAreJoined(t *testing.T) {
	if code, _, _ := runSS("check", "rm", "-rf", "build"); code != 3 {
		t.Errorf("unquoted words should be joined and rated; exit = %d", code)
	}
}

func TestReportContents(t *testing.T) {
	_, out, _ := runSS("check", "rm -rf ~/projects")
	for _, want := range []string{"R3 Irreversible", "BLOCK", "Safer options", "trash-put ~/projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\033[") {
		t.Error("colour codes written when colour is off")
	}
}

func TestColourWhenEnabled(t *testing.T) {
	var o, e bytes.Buffer
	run([]string{"check", "rm -rf x"}, &o, &e, true)
	if !strings.Contains(o.String(), "\033[") {
		t.Error("expected colour codes")
	}
}

func TestJSONOutput(t *testing.T) {
	code, out, _ := runSS("check", "--json", "rm -rf /")
	if code != 4 {
		t.Errorf("exit = %d, want 4", code)
	}
	var v struct {
		Level    string
		Findings []struct{ Level, Reason string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if v.Level != "R4" || len(v.Findings) == 0 {
		t.Errorf("unexpected JSON: %+v", v)
	}
}

func TestUsageAndErrors(t *testing.T) {
	if code, out, _ := runSS(); code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("no args should print usage and exit 0, got %d %q", code, out)
	}
	if code, _, e := runSS("check"); code != 2 || !strings.Contains(e, "Usage:") {
		t.Errorf("check with no command should exit 2 with usage, got %d", code)
	}
	if code, _, e := runSS("bogus"); code != 2 || !strings.Contains(e, `unknown command "bogus"`) {
		t.Errorf("unknown subcommand: %d %q", code, e)
	}
}

func TestDoctor(t *testing.T) {
	old := probe
	defer func() { probe = old }()

	probe = func() platform.Report {
		return platform.Report{Kernel: "6.8.0", UserNS: true, Overlay: true, Seccomp: true, Landlock: true, CgroupV2: true, RootFS: "ext4", Shells: []string{"bash", "zsh"}}
	}
	code, out, _ := runSS("doctor")
	if code != 0 || !strings.Contains(out, "available") || !strings.Contains(out, "copy (root is ext4)") || !strings.Contains(out, "bash, zsh") {
		t.Errorf("healthy doctor: exit %d\n%s", code, out)
	}

	probe = func() platform.Report {
		return platform.Report{Kernel: "6.8.0", Overlay: true, AppArmorRestrictsUserNS: true, UserNSErr: "fork/exec: operation not permitted"}
	}
	code, out, _ = runSS("doctor")
	if code != 1 || !strings.Contains(out, "NOT available") || !strings.Contains(out, "MISSING  user namespaces") || !strings.Contains(out, "install-apparmor.sh") {
		t.Errorf("blocked doctor: exit %d\n%s", code, out)
	}
}

func fakeSim(t *testing.T, res sandbox.Result, err error) *sandbox.Spec {
	t.Helper()
	old := simulate
	t.Cleanup(func() { simulate = old })
	var got sandbox.Spec
	simulate = func(_ context.Context, spec sandbox.Spec) (sandbox.Result, error) {
		got = spec
		return res, err
	}
	return &got
}

func TestSimulateReport(t *testing.T) {
	got := fakeSim(t, sandbox.Result{
		ExitCode: 0, Duration: 12 * time.Millisecond, Stdout: "hello\n",
		Diff: sandbox.FileDiff{Changes: []sandbox.Change{
			{Path: "home/me/proj", Kind: sandbox.Deleted, IsDir: true, Files: 4312, Bytes: 2 << 30},
			{Path: "tmp/new.txt", Kind: sandbox.Created, Files: 1, Bytes: 5},
		}},
		Warnings: []sandbox.Warning{{Paths: []string{"/work/proj/cache"}, Text: "/work/proj/cache is not available"}, {Paths: []string{"/var/lib/docker"}, Text: "/var/lib/docker is not available"}},
	}, nil)
	code, out, _ := runSS("simulate", "--cwd", "/work", "--timeout", "5s", "rm", "-rf", "/work/proj")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got.Line != "rm -rf /work/proj" || got.Cwd != "/work" || got.Timeout != 5*time.Second {
		t.Errorf("spec passed to the sandbox: %+v", got)
	}
	for _, want := range []string{
		"Your real files were not touched", "deleted   4,312 files (2.0 GiB)", "created   1 file",
		"/home/me/proj/  (4,312 files)", "Warning: /work/proj/cache", "1 sandbox note about other folders was hidden", "stdout", "  hello",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestSimulateListsAtMostFifteenChanges(t *testing.T) {
	var cs []sandbox.Change
	for i := 0; i < 20; i++ {
		cs = append(cs, sandbox.Change{Path: "f", Kind: sandbox.Created, Files: 1})
	}
	fakeSim(t, sandbox.Result{Diff: sandbox.FileDiff{Changes: cs}}, nil)
	_, out, _ := runSS("simulate", "x")
	if !strings.Contains(out, "... and 5 more") {
		t.Errorf("long change lists must be cut:\n%s", out)
	}
}

func TestSimulateTimedOutAndJSON(t *testing.T) {
	fakeSim(t, sandbox.Result{TimedOut: true, ExitCode: -1}, nil)
	if _, out, _ := runSS("simulate", "sleep 99"); !strings.Contains(out, "timed out") {
		t.Errorf("timeout not reported:\n%s", out)
	}
	code, out, _ := runSS("simulate", "--json", "x")
	var r sandbox.Result
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || !r.TimedOut {
		t.Errorf("json output: %d %s", code, out)
	}
}

func TestSimulateErrors(t *testing.T) {
	fakeSim(t, sandbox.Result{}, errors.New("sandbox setup failed: nope"))
	if code, _, e := runSS("simulate", "x"); code != 1 || !strings.Contains(e, "sandbox setup failed") {
		t.Errorf("runner error: %d %q", code, e)
	}
	for _, args := range [][]string{{"simulate"}, {"simulate", "--cwd"}, {"simulate", "--bogus", "x"}, {"simulate", "--timeout", "soon", "x"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

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
