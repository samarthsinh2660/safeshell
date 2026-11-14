package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// realLoadPolicy is the real loader, kept by TestMain before it installs a fake.
var realLoadPolicy func() ([]policy.Rule, bool, error)

// fakePolicy makes the team policy what a test says it is.
func fakePolicy(t *testing.T, rules []policy.Rule, found bool, err error) {
	t.Helper()
	old := loadPolicy
	t.Cleanup(func() { loadPolicy = old })
	loadPolicy = func() ([]policy.Rule, bool, error) { return rules, found, err }
}

var blockSecret = []policy.Rule{{Match: []string{"echo", "secret"}, Block: "Secrets stay out of shell history."}}

func TestTeamPolicyBlocksEvenWhenTheUserSaysDoItAnyway(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakePolicy(t, blockSecret, true, nil)
	answer(t, "d\nyes\ny\n")
	code, out, errOut := runSS("guard", "--", "echo secret")
	if code != 1 || out != "" {
		t.Errorf("code %d out %q", code, out)
	}
	wantAll(t, errOut, "Blocked by your team's policy: Secrets stay out of shell history.", "BLOCK", "Ask your administrator")
	if strings.Contains(errOut, "do it anyway") || strings.Contains(errOut, "you can override") {
		t.Errorf("the policy block must not offer an override:\n%s", errOut)
	}
	if e := lastAudit(t); e.Outcome != "cancelled" || e.Decision != "block" || e.Line != "echo secret" {
		t.Errorf("%+v", e)
	}
	if code, out, _ := runSS("guard", "--", "echo hello"); code != 0 || out != "echo hello\n" {
		t.Errorf("other commands are untouched: code %d out %q", code, out)
	}
}

func TestTeamPolicyRaisesTheLevelInAnalyze(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakePolicy(t, []policy.Rule{{Match: []string{"echo"}, MinLevel: domain.R3}}, true, nil)
	code, out, _ := runSS("analyze", "echo", "hi")
	if code != 3 || !strings.Contains(out, "R3 Irreversible  BLOCK (you can override)") {
		t.Errorf("code %d out %s", code, out)
	}
	fakePolicy(t, blockSecret, true, nil)
	code, out, _ = runSS("analyze", "echo", "secret")
	if code != 3 || !strings.Contains(out, "R0 Read-only  BLOCK\nBlocked by your team's policy: Secrets stay out of shell history.") {
		t.Errorf("code %d out %s", code, out)
	}
}

func TestBrokenPolicyWarnsOnEveryCheckAndRulesStillApply(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakePolicy(t, nil, true, errors.New("the team policy /etc/safeshell/policy.json was ignored: broken"))
	answer(t, "")
	code, out, errOut := runSS("guard", "--", "echo hi")
	if code != 0 || out != "echo hi\n" || !strings.Contains(errOut, "Warning: the team policy /etc/safeshell/policy.json was ignored: broken") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
	code, _, errOut = runSS("analyze", "echo", "hi")
	if code != 0 || !strings.Contains(errOut, "Warning: the team policy") {
		t.Errorf("code %d err %q", code, errOut)
	}
	fakeAnalyze(t, sampleAnalysis())
	answer(t, "\n")
	if code, _, _ := runSS("guard", "--", "rm -rf ~/projects"); code != 1 {
		t.Error("a broken policy must not loosen the normal rules")
	}
}

func TestSnapshotRuleSaysWhenNothingCouldBeSaved(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R1, domain.Confirm
	a.Team = policy.Outcome{Snapshot: true}
	fakeAnalyze(t, a)
	answer(t, "y\n")
	code, out, errOut := runSS("guard", "--", "rm -rf ~/projects")
	if code != 0 || out != "rm -rf ~/projects\n" || !strings.Contains(errOut, "Your team's policy asks for a snapshot first, but SafeShell could not tell which files") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestDoctorShowsTheTeamPolicy(t *testing.T) {
	doctorHome(t)
	fakePolicy(t, nil, false, nil)
	wantAll(t, doctorOut(t), "info     team policy", "none set", config.PolicyPath)
	fakePolicy(t, append(blockSecret, policy.Rule{Match: []string{"rm"}, MinLevel: domain.R3}), true, nil)
	wantAll(t, doctorOut(t), "ok       team policy", config.PolicyPath+", 2 rules")
	fakePolicy(t, nil, true, errors.New("the team policy was ignored because it could be changed by someone else"))
	wantAll(t, doctorOut(t), "MISSING  team policy", "could be changed by someone else")
}

func TestDefaultLoaderReadsOnlyTheAdminsFile(t *testing.T) {
	rules, _, err := realLoadPolicy()
	if err != nil && rules != nil {
		t.Errorf("a refused policy must give no rules: %v", rules)
	}
}
