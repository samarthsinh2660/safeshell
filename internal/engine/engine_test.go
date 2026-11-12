package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

type fakeSim struct {
	res   sandbox.Result
	err   error
	calls []sandbox.Spec
}

func (f *fakeSim) Simulate(_ context.Context, s sandbox.Spec) (sandbox.Result, error) {
	f.calls = append(f.calls, s)
	return f.res, f.err
}

func del(path string, files int) sandbox.Result {
	return sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: path, Kind: sandbox.Deleted, IsDir: true, Files: files, Bytes: int64(files)}}}}
}

func find(a Analysis, k domain.EffectKind) *domain.Effect {
	for i := range a.Effects {
		if a.Effects[i].Kind == k {
			return &a.Effects[i]
		}
	}
	return nil
}

func TestHarmlessCommandsAreNotSimulated(t *testing.T) {
	sim := &fakeSim{}
	e := &Engine{Sim: sim}
	for _, line := range []string{"ls -la", "cat /etc/hostname", "mkdir -p x", "mv a b", "git status"} {
		a := e.Analyze(context.Background(), line, "/w")
		if a.Decision != domain.Allow || a.Sim != nil {
			t.Errorf("%q: decision %s sim=%v", line, a.Decision, a.Sim != nil)
		}
	}
	if len(sim.calls) != 0 {
		t.Errorf("the sandbox must not run for R0/R1 commands: %v", sim.calls)
	}
}

func TestDestructiveCommandIsSimulatedAndObservedImpactIsUsed(t *testing.T) {
	sim := &fakeSim{res: del("home/me/projects", 4312)}
	e := &Engine{Sim: sim}
	a := e.Analyze(context.Background(), "rm -rf ~/projects", "/home/me")
	if len(sim.calls) != 1 || sim.calls[0].Line != "rm -rf ~/projects" || sim.calls[0].Cwd != "/home/me" {
		t.Fatalf("sandbox call: %+v", sim.calls)
	}
	fs := find(a, domain.EffectFS)
	if fs == nil || fs.Evidence != domain.Observed || !strings.Contains(fs.Summary, "delete 4,312 files") {
		t.Fatalf("file effect: %+v", fs)
	}
	if a.Level != domain.R3 || a.Decision != domain.Block || a.Sim == nil {
		t.Errorf("level %s decision %s", a.Level, a.Decision)
	}
}

func TestObservedImpactCanRaiseTheRuleLevel(t *testing.T) {
	// `rm file` is R2 by its text, but if what it deletes is a git history the
	// observed result makes it R3.
	sim := &fakeSim{res: sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: "home/me/proj/.git/HEAD", Kind: sandbox.Deleted, Files: 1}}}}}
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "rm proj/.git/HEAD", "/home/me")
	if a.Verdict.Level != domain.R2 || a.Level != domain.R3 || a.Decision != domain.Block {
		t.Errorf("rule %s final %s decision %s", a.Verdict.Level, a.Level, a.Decision)
	}
}

func TestObservedImpactNeverLowersTheRuleLevel(t *testing.T) {
	sim := &fakeSim{res: sandbox.Result{}} // the run changed nothing
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "rm -rf build", "/w")
	if a.Level != domain.R3 {
		t.Errorf("an empty observation must not soften an R3 command: %s", a.Level)
	}
}

func TestCatastrophicCommandIsNotSimulated(t *testing.T) {
	sim := &fakeSim{}
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "rm -rf /", "/")
	if len(sim.calls) != 0 || a.Decision != domain.HardBlock || !strings.Contains(a.SimNote, "catastrophic") {
		t.Errorf("calls=%d decision=%s note=%q", len(sim.calls), a.Decision, a.SimNote)
	}
}

func TestUnknownCommandsGoToTheSandbox(t *testing.T) {
	sim := &fakeSim{res: sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: "tmp/out", Kind: sandbox.Created, Files: 1}}}}}
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "mystery-tool --go", "/w")
	if len(sim.calls) != 1 || a.Verdict.Level != domain.R1 {
		t.Fatalf("calls %d level %s", len(sim.calls), a.Verdict.Level)
	}
	if a.Decision != domain.Allow {
		t.Errorf("a new file is fine, got %s", a.Decision)
	}
}

func TestNoSandboxMeansEstimatedAndNeverAutoRuns(t *testing.T) {
	a := (&Engine{}).Analyze(context.Background(), "rm file", "/w")
	fs := find(a, domain.EffectFS)
	if fs == nil || fs.Evidence != domain.Estimated || !strings.Contains(a.SimNote, "no sandbox") {
		t.Errorf("%+v note=%q", fs, a.SimNote)
	}
	// an unknown command (R1) with no sandbox must not simply be allowed
	a = (&Engine{}).Analyze(context.Background(), "mystery-tool", "/w")
	if a.Decision != domain.Confirm {
		t.Errorf("unknown + no sandbox should need confirmation, got %s", a.Decision)
	}
}

func TestSimulationFailureIsHonest(t *testing.T) {
	a := (&Engine{Sim: &fakeSim{err: errors.New("boom")}}).Analyze(context.Background(), "rm file", "/w")
	if a.Sim != nil || !strings.Contains(a.SimNote, "boom") || find(a, domain.EffectFS).Evidence != domain.Estimated {
		t.Errorf("%+v", a)
	}
}

func TestTimeoutAddsAnEstimatedEffect(t *testing.T) {
	res := sandbox.Result{TimedOut: true, Duration: 30 * time.Second}
	a := (&Engine{Sim: &fakeSim{res: res}}).Analyze(context.Background(), "rm -rf big", "/w")
	var est int
	for _, f := range a.Effects {
		if f.Evidence == domain.Estimated {
			est++
		}
	}
	if est != 1 || !strings.Contains(a.SimNote, "timed out") {
		t.Errorf("estimated=%d note=%q", est, a.SimNote)
	}
}

func TestInspectorsFeedTheDecision(t *testing.T) {
	var got []shellparse.Command
	inspect := func(cmds []shellparse.Command) []domain.Effect {
		got = cmds
		return []domain.Effect{{Kind: domain.EffectProcess, Evidence: domain.Inspected, Summary: "would signal your shell", MinLevel: domain.R4}}
	}
	a := (&Engine{Inspectors: []Inspector{inspect}}).Analyze(context.Background(), "echo x && kill -9 100", "/w")
	if len(got) != 2 || got[1].Name != "kill" {
		t.Errorf("inspectors must receive the parsed commands: %+v", got)
	}
	if a.Verdict.Level != domain.R2 || a.Level != domain.R4 || a.Decision != domain.HardBlock {
		t.Errorf("rule %s final %s decision %s", a.Verdict.Level, a.Level, a.Decision)
	}
}

func TestUnparsableLineDoesNotCrash(t *testing.T) {
	called := false
	e := &Engine{Inspectors: []Inspector{func(c []shellparse.Command) []domain.Effect { called = len(c) == 0; return nil }}}
	a := e.Analyze(context.Background(), "echo 'oops", "/w")
	if !called || a.Verdict.Level != domain.R2 {
		t.Errorf("called=%v level=%s", called, a.Verdict.Level)
	}
}

func gap(paths ...string) []sandbox.Warning {
	return []sandbox.Warning{{Paths: paths, Text: "loose files are not visible in the sandbox"}}
}

func estimatedCount(a Analysis) int {
	n := 0
	for _, f := range a.Effects {
		if f.Evidence == domain.Estimated {
			n++
		}
	}
	return n
}

func TestMirrorGap(t *testing.T) {
	tests := []struct {
		name, line string
		ws         []sandbox.Warning
		wantEst    int
		want       domain.Decision
	}{
		{"unknown command, gap covers cwd", "./t.sh", gap("/home/me"), 1, domain.Confirm},
		{"unknown command, gap elsewhere", "mystery-tool", gap("/mnt/disk"), 1, domain.Confirm},
		{"unknown command, no gap", "mystery-tool", nil, 0, domain.Allow},
		{"known command, gap covers cwd", "rm a.txt", gap("/home/me"), 1, domain.Confirm},
		{"known command, gap covers a named path", "rm /mnt/disk/a.txt", gap("/mnt/disk"), 1, domain.Confirm},
		{"known command, gap elsewhere", "rm /home/me/proj/a.txt", gap("/mnt/disk"), 0, domain.Confirm},
		{"known command, paths unknowable", "rm ~/a.txt", gap("/mnt/disk"), 1, domain.Confirm},
		{"known command, no gap", "rm a.txt", nil, 0, domain.Confirm},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sim := &fakeSim{res: sandbox.Result{Warnings: tc.ws}}
			a := (&Engine{Sim: sim}).Analyze(context.Background(), tc.line, "/home/me/proj")
			if got := estimatedCount(a); got != tc.wantEst || a.Decision != tc.want {
				t.Errorf("estimated=%d decision=%s, want %d %s", got, a.Decision, tc.wantEst, tc.want)
			}
		})
	}
}

func TestMirrorGapKeepsTheObservedEffectAndLevel(t *testing.T) {
	sim := &fakeSim{res: sandbox.Result{Warnings: gap("/home/me")}}
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "./t.sh", "/home/me/proj")
	if a.Level != domain.R1 || a.Decision != domain.Confirm {
		t.Errorf("level %s decision %s", a.Level, a.Decision)
	}
	if e := find(a, domain.EffectFS); e == nil || e.Evidence != domain.Observed {
		t.Errorf("the observed file effect should stay: %+v", e)
	}
}
