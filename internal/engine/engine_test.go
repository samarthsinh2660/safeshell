package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/policy"
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

func gap(writable ...string) []sandbox.Warning {
	return []sandbox.Warning{{Paths: []string{"/home/me", "/boot"}, Text: "loose files are not visible in the sandbox", Writable: writable}}
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

func TestMirrorGapOnlyCountsWritableGapsForUnknownCommands(t *testing.T) {
	tests := []struct {
		name, line string
		ws         []sandbox.Warning
		wantEst    int
		want       domain.Decision
	}{
		{"unknown command, writable gap", "./t.sh", gap("/home/me"), 1, domain.Confirm},
		{"unknown command, gaps but none writable", "./t.sh", gap(), 0, domain.Allow},
		{"unknown command, no gaps", "./t.sh", nil, 0, domain.Allow},
		{"known command, writable gap", "rm a.txt", gap("/home/me"), 0, domain.Confirm},
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

func TestTheCallersPidTravelsInTheContext(t *testing.T) {
	if got := CallerFrom(context.Background()); got != 0 {
		t.Errorf("no caller was attached, got %d", got)
	}
	if got := CallerFrom(WithCaller(context.Background(), 4242)); got != 4242 {
		t.Errorf("got %d", got)
	}
}

func TestTheCallersEnvironmentReachesTheSandbox(t *testing.T) {
	sim := &fakeSim{res: del("x", 1)}
	e := &Engine{Sim: sim}
	env := []string{"PATH=/venv/bin", "HOME=/home/me"}
	e.Analyze(WithEnv(context.Background(), env), "rm -rf x", "/w")
	if len(sim.calls) != 1 || strings.Join(sim.calls[0].Env, ",") != strings.Join(env, ",") {
		t.Errorf("env %v", sim.calls)
	}
	e.Analyze(context.Background(), "rm -rf x", "/w")
	if sim.calls[1].Env != nil {
		t.Errorf("no env was attached, got %v", sim.calls[1].Env)
	}
}

func TestNetworkAttemptsInTheSandboxBecomeAnEffect(t *testing.T) {
	res := del("x", 1)
	res.Net = []sandbox.NetAttempt{{Syscall: "connect", Family: "inet", Addr: "203.0.113.7", Port: 443, Count: 1}}
	a := (&Engine{Sim: &fakeSim{res: res}}).Analyze(context.Background(), "rm -rf x", "/w")
	ne := find(a, domain.EffectNetwork)
	if ne == nil || ne.Evidence != domain.Observed || ne.MinLevel != domain.R2 || len(ne.Details) != 1 {
		t.Fatalf("%+v", a.Effects)
	}
	res.Net = []sandbox.NetAttempt{{Syscall: "connect", Family: "unix", Addr: "/run/nscd/socket", Count: 1}}
	a = (&Engine{Sim: &fakeSim{res: res}}).Analyze(context.Background(), "rm -rf x", "/w")
	if find(a, domain.EffectNetwork) != nil {
		t.Error("a libc name-service lookup is not worth showing")
	}
}

func TestTeamPolicyBlocksAndSurvivesLaterEffects(t *testing.T) {
	rules := []policy.Rule{{Match: []string{"docker", "system", "prune"}, Block: "Ask the platform team."}}
	a := (&Engine{Rules: rules}).Analyze(context.Background(), "docker system prune -af", "/w")
	if a.Team.Block != "Ask the platform team." || a.Decision < domain.Block {
		t.Fatalf("%+v %s", a.Team, a.Decision)
	}
	a.AddEffect(domain.Effect{Kind: domain.EffectReview, Evidence: domain.Observed, MinLevel: domain.R0})
	if a.Decision < domain.Block {
		t.Error("an AI review must not lift a policy block")
	}
	if b := (&Engine{Rules: rules}).Analyze(context.Background(), "docker ps", "/w"); b.Team.Block != "" || b.Decision != domain.Allow {
		t.Errorf("an unrelated command is untouched: %+v %s", b.Team, b.Decision)
	}
}

func TestTeamPolicyMinimumLevelRaisesTheDecision(t *testing.T) {
	rules := []policy.Rule{{Match: []string{"ls"}, MinLevel: domain.R3}}
	a := (&Engine{Rules: rules}).Analyze(context.Background(), "ls", "/w")
	if a.Level != domain.R3 || a.Decision != domain.Block || a.Verdict.Level != domain.R0 {
		t.Errorf("%s %s (rules said %s)", a.Level, a.Decision, a.Verdict.Level)
	}
}

func TestAddEffectCanOnlyRaiseTheLevel(t *testing.T) {
	a := (&Engine{}).Analyze(context.Background(), "ls", "/w")
	if a.Level != domain.R0 || a.Decision != domain.Allow {
		t.Fatalf("%s %s", a.Level, a.Decision)
	}
	a.AddEffect(domain.Effect{Kind: domain.EffectReview, Evidence: domain.Observed, MinLevel: domain.R3})
	if a.Level != domain.R3 || a.Decision != domain.Block || len(a.Effects) != 1 {
		t.Errorf("raise: %s %s %v", a.Level, a.Decision, a.Effects)
	}
	a.AddEffect(domain.Effect{Kind: domain.EffectReview, Evidence: domain.Observed, MinLevel: domain.R0})
	if a.Level != domain.R3 || a.Decision != domain.Block {
		t.Errorf("a lower opinion lowered it: %s %s", a.Level, a.Decision)
	}
}

func TestASnapshotRuleRunsTheSandboxEvenForALowRiskCommand(t *testing.T) {
	sim := &fakeSim{}
	rules := []policy.Rule{{Match: []string{"touch"}, Snapshot: true}}
	a := (&Engine{Sim: sim, Rules: rules}).Analyze(context.Background(), "touch notes.txt", "/w")
	if len(sim.calls) != 1 || a.Sim == nil || a.Decision < domain.Confirm {
		t.Errorf("calls %d sim %v decision %s: a snapshot needs the files the run changes", len(sim.calls), a.Sim, a.Decision)
	}
	if b := (&Engine{Sim: &fakeSim{}}).Analyze(context.Background(), "touch notes.txt", "/w"); b.Sim != nil {
		t.Error("without the rule a low-risk command is not simulated")
	}
}
