package engine

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/policy"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func r2Analysis(t *testing.T) Analysis {
	t.Helper()
	sim := &fakeSim{res: sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: "w/build/x.o", Kind: sandbox.Deleted, Files: 1, Bytes: 1}}}}}
	a := (&Engine{Sim: sim}).Analyze(context.Background(), "rm build/x.o", "/w")
	if a.Level != domain.R2 || a.Decision != domain.Confirm || a.Effects[0].Evidence != domain.Observed {
		t.Fatalf("the starting point is a plain observed R2 confirmation: %s %s %+v", a.Level, a.Decision, a.Effects)
	}
	return a
}

func TestTrustTurnsAnObservedR2ConfirmIntoAllowOnlyForThatLineAndFolder(t *testing.T) {
	set := []policy.Trust{policy.NewTrust("rm build/x.o", "/w")}
	a := r2Analysis(t)
	if !a.CanTrust("/w") {
		t.Error("it should be offered")
	}
	a.ApplyTrust(set, "/w")
	if a.Decision != domain.Allow || !a.Trusted || a.Level != domain.R2 {
		t.Errorf("%s trusted=%v level=%s", a.Decision, a.Trusted, a.Level)
	}
	b := r2Analysis(t)
	b.ApplyTrust(set, "/other")
	if b.Decision != domain.Confirm || b.Trusted {
		t.Errorf("another folder: %s", b.Decision)
	}
	c := r2Analysis(t)
	c.Line = "rm build/y.o"
	c.ApplyTrust(set, "/w")
	if c.Decision != domain.Confirm || c.Trusted {
		t.Errorf("another line: %s", c.Decision)
	}
}

func TestALineThatIsAlreadyAllowedIsNotMarkedTrusted(t *testing.T) {
	a := r2Analysis(t)
	a.Verdict.Level, a.Level, a.Decision = domain.R1, domain.R1, domain.Allow
	a.ApplyTrust([]policy.Trust{policy.NewTrust(a.Line, "/w")}, "/w")
	if a.Trusted {
		t.Error("an allowed line was not trusted into anything")
	}
}

func TestTrustIsNeverOfferedOrAppliedWhenAnythingElseHadASay(t *testing.T) {
	set := []policy.Trust{policy.NewTrust("rm build/x.o", "/w")}
	for name, change := range map[string]func(*Analysis){
		"team rule":        func(a *Analysis) { a.Team = policy.Outcome{Snapshot: true} },
		"team min level":   func(a *Analysis) { a.Team = policy.Outcome{MinLevel: domain.R2} },
		"prod host":        func(a *Analysis) { a.Host = policy.HostOutcome{MinLevel: domain.R2} },
		"protected folder": func(a *Analysis) { a.Protected = policy.ProtectOutcome{Reason: "protected"} },
		"policy warning":   func(a *Analysis) { a.PolicyWarning = "the policy could not be read" },
		"AI raise": func(a *Analysis) {
			a.AI = &AIReview{Raised: true, Level: domain.R3}
			a.AddEffect(domain.Effect{Kind: domain.EffectFS, Evidence: domain.Inspected, MinLevel: domain.R3})
		},
		"unrecognised command": func(a *Analysis) { a.Verdict.Unclassified = []string{"mytool"} },
		"estimated effect": func(a *Analysis) {
			a.AddEffect(domain.Effect{Kind: domain.EffectFS, Evidence: domain.Estimated, MinLevel: domain.R2})
		},
		"inspected effect": func(a *Analysis) {
			a.AddEffect(domain.Effect{Kind: domain.EffectFS, Evidence: domain.Inspected, MinLevel: domain.R2})
		},
		"R3 from the rules": func(a *Analysis) { a.Verdict.Level, a.Level, a.Decision = domain.R3, domain.R3, domain.Block },
		"R4":                func(a *Analysis) { a.Verdict.Level, a.Level, a.Decision = domain.R4, domain.R4, domain.HardBlock },
	} {
		a := r2Analysis(t)
		change(&a)
		if a.CanTrust("/w") {
			t.Errorf("%s: offered", name)
		}
		before := a.Decision
		a.ApplyTrust(set, "/w")
		if a.Decision != before || a.Trusted {
			t.Errorf("%s: trust changed %s to %s", name, before, a.Decision)
		}
	}
}

func TestATrustedAnalysisSurvivesTheDaemonRoundTrip(t *testing.T) {
	a := r2Analysis(t)
	a.ApplyTrust([]policy.Trust{policy.NewTrust(a.Line, "/w")}, "/w")
	b, _ := json.Marshal(a)
	var back Analysis
	if err := json.Unmarshal(b, &back); err != nil || !back.Trusted || back.Decision != domain.Allow {
		t.Errorf("%+v %v", back, err)
	}
	if back.CanTrust("/w") {
		t.Error("an allowed line has nothing to confirm, so nothing is offered")
	}
}
