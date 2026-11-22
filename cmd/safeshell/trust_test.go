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
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// r2Sample is a plain R2 confirmation whose effects were observed in the sandbox.
func r2Sample(files ...string) engine.Analysis {
	a := sampleAnalysis()
	a.Line = "rm build/x.o"
	a.Verdict = domain.Verdict{Level: domain.R2}
	a.Level, a.Decision = domain.R2, domain.Confirm
	a.Effects = []domain.Effect{{Kind: domain.EffectFS, Evidence: domain.Observed, Summary: "would delete 1 file", MinLevel: domain.R2}}
	a.Sim = &sandbox.Result{}
	for _, f := range files {
		a.Sim.Diff.Changes = append(a.Sim.Diff.Changes, sandbox.Change{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Deleted})
	}
	return a
}

// trustHome gives the test its own settings folder and returns the settings path.
func trustHome(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	return config.Path()
}

func trustedNow(t *testing.T) []policy.Trust {
	t.Helper()
	c, err := config.Load(config.Path())
	if err != nil {
		t.Fatal(err)
	}
	return c.Trusted
}

func TestAnsweringAMakesTheNextRunQuietButStillSnapshotsAndAudits(t *testing.T) {
	trustHome(t)
	useStore(t)
	f := filepath.Join(t.TempDir(), "x.o")
	os.WriteFile(f, []byte("object"), 0o644)
	fakeAnalyze(t, r2Sample(f))

	answer(t, "a\n")
	code, out, errOut := runSS("guard", "--cwd", "/work", "--", "rm build/x.o")
	if code != 0 || out != "rm build/x.o\n" || !strings.Contains(errOut, "[y/N/a]") || !strings.Contains(errOut, "Saved. SafeShell will not ask again") {
		t.Fatalf("first run: code %d out %q err %q", code, out, errOut)
	}
	if e := lastAudit(t); e.Outcome != "ran" || e.Decision != "confirm" {
		t.Errorf("the first run was confirmed by the person: %+v", e)
	}
	if got := trustedNow(t); len(got) != 1 || got[0] != (policy.Trust{Line: "rm build/x.o", Dir: "/work"}) {
		t.Fatalf("saved %+v", got)
	}

	answer(t, "") // nobody to ask: a question here would cancel
	code, out, errOut = runSS("guard", "--cwd", "/work", "--", "rm build/x.o")
	if code != 0 || out != "rm build/x.o\n" || !strings.Contains(errOut, "always run this command in this folder") || strings.Contains(errOut, "Run it anyway") {
		t.Fatalf("second run: code %d out %q err %q", code, out, errOut)
	}
	if !strings.Contains(errOut, "Snapshot saved") {
		t.Errorf("the snapshot is still taken: %q", errOut)
	}
	if e := lastAudit(t); e.Outcome != "trusted" || e.Line != "rm build/x.o" || e.Level != "R2" || e.Decision != "allow" {
		t.Errorf("the audit still records the line: %+v", e)
	}
	if _, out, _ := runSS("snapshots"); !strings.Contains(out, "rm build/x.o") {
		t.Errorf("no snapshot listed: %q", out)
	}
}

func TestTrustDoesNotCarryToAnotherFolderOrLine(t *testing.T) {
	trustHome(t)
	fakeAnalyze(t, r2Sample())
	answer(t, "a\n")
	runSS("guard", "--cwd", "/work", "--", "rm build/x.o")
	for _, args := range [][]string{{"--cwd", "/other", "--", "rm build/x.o"}, {"--cwd", "/work", "--", "rm build/x.o"}} {
		a := r2Sample()
		if args[1] == "/work" {
			a.Line = "rm build/y.o"
			args[3] = "rm build/y.o"
		}
		fakeAnalyze(t, a)
		answer(t, "\n")
		if code, out, errOut := runSS(append([]string{"guard"}, args...)...); code != 1 || out != "" || !strings.Contains(errOut, "Run it anyway") {
			t.Errorf("%v: code %d out %q err %q", args, code, out, errOut)
		}
	}
}

func TestTrustIsNotOfferedOrUsedWhenSomethingElseHadASay(t *testing.T) {
	trustHome(t)
	work := "/work"
	if err := config.SetTrusted(config.Path(), []policy.Trust{{Line: "rm build/x.o", Dir: work}}); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*engine.Analysis){
		"R3": func(a *engine.Analysis) { a.Verdict.Level, a.Level, a.Decision = domain.R3, domain.R3, domain.Block },
		"R4": func(a *engine.Analysis) {
			a.Verdict.Level, a.Level, a.Decision = domain.R4, domain.R4, domain.HardBlock
		},
		"estimated": func(a *engine.Analysis) { a.Effects[0].Evidence = domain.Estimated },
		"team rule": func(a *engine.Analysis) { a.Team = policy.Outcome{Snapshot: true} },
		"protected folder": func(a *engine.Analysis) {
			a.Protected = policy.ProtectOutcome{MinLevel: domain.R2, Reason: "protected"}
		},
		"prod host": func(a *engine.Analysis) { a.Host = policy.HostOutcome{MinLevel: domain.R2} },
	} {
		a := r2Sample()
		change(&a)
		fakeAnalyze(t, a)
		answer(t, "a\n")
		_, out, errOut := runSS("guard", "--cwd", work, "--", "rm build/x.o")
		if strings.Contains(errOut, "always run this command in this folder") || strings.Contains(errOut, "[y/N/a]") {
			t.Errorf("%s: trust was offered or used: %q", name, errOut)
		}
		if a.Decision == domain.Confirm && out != "" {
			t.Errorf("%s: ran without a plain yes: %q", name, out)
		}
		if e := lastAudit(t); e.Outcome == "trusted" {
			t.Errorf("%s: %+v", name, e)
		}
	}
	if got := trustedNow(t); len(got) != 1 {
		t.Errorf("answering a at a prompt that did not offer it must save nothing: %+v", got)
	}
}

func TestAnAIRaiseBeatsTrust(t *testing.T) {
	trustHome(t)
	if err := config.SetTrusted(config.Path(), []policy.Trust{{Line: "rm build/x.o", Dir: "/work"}}); err != nil {
		t.Fatal(err)
	}
	a := r2Sample()
	a.AI = &engine.AIReview{Provider: "x", Level: domain.R3, Raised: true}
	a.Level, a.Decision = domain.R3, domain.Block
	fakeAnalyze(t, a)
	answer(t, "\n")
	if code, _, errOut := runSS("guard", "--cwd", "/work", "--", "rm build/x.o"); code != 1 || strings.Contains(errOut, "always run") {
		t.Errorf("code %d err %q", code, errOut)
	}
}

func TestTrustIsOffInStrictModeAndWhenThePolicyOrSettingsCannotBeRead(t *testing.T) {
	old := teamFile
	t.Cleanup(func() { teamFile = old })
	for name, tc := range map[string]struct {
		cfg  string
		team func() (policy.File, bool, error)
	}{
		"strict setting":   {`{"strict":true,"trusted":[{"line":"rm build/x.o","dir":"/work"}]}`, nil},
		"strict team":      {`{"trusted":[{"line":"rm build/x.o","dir":"/work"}]}`, func() (policy.File, bool, error) { return policy.File{Strict: true}, true, nil }},
		"team no_trust":    {`{"trusted":[{"line":"rm build/x.o","dir":"/work"}]}`, func() (policy.File, bool, error) { return policy.File{NoTrust: true}, true, nil }},
		"team unreadable":  {`{"trusted":[{"line":"rm build/x.o","dir":"/work"}]}`, func() (policy.File, bool, error) { return policy.File{}, true, errors.New("bad policy") }},
		"settings damaged": {`{"trusted":`, nil},
	} {
		p := trustHome(t)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(tc.cfg), 0o600)
		teamFile = old
		if tc.team != nil {
			teamFile = tc.team
		}
		fakeAnalyze(t, r2Sample())
		answer(t, "a\n")
		code, out, errOut := runSS("guard", "--cwd", "/work", "--", "rm build/x.o")
		if code != 1 || out != "" || strings.Contains(errOut, "always run") || strings.Contains(errOut, "[y/N/a]") || !strings.Contains(errOut, "Run it anyway? [y/N] ") {
			t.Errorf("%s: code %d out %q err %q", name, code, out, errOut)
		}
	}
}

func TestTrustNeverAppliesToAnAIAgent(t *testing.T) {
	trustHome(t)
	if err := config.SetTrusted(config.Path(), []policy.Trust{{Line: "rm build/x.o", Dir: "/work"}}); err != nil {
		t.Fatal(err)
	}
	if ans := answerFor(r2Sample()); ans.Allow {
		t.Errorf("an agent is never answered with a trusted confirmation: %+v", ans)
	}
}

func TestSavingTrustTwiceKeepsOneEntryAndAFailureIsSaid(t *testing.T) {
	trustHome(t)
	var out strings.Builder
	tr := policy.NewTrust("rm build/x.o", "/work")
	saveTrust(tr, &out)
	saveTrust(tr, &out)
	if got := trustedNow(t); len(got) != 1 || strings.Count(out.String(), "Saved.") != 1 {
		t.Errorf("%+v %q", got, out.String())
	}
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	dir := filepath.Dir(config.Path())
	os.Chmod(dir, 0o500)
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	out.Reset()
	saveTrust(policy.NewTrust("rm build/y.o", "/work"), &out)
	if !strings.Contains(out.String(), "could not save that choice") {
		t.Errorf("%q", out.String())
	}
}

func TestTrustCommand(t *testing.T) {
	trustHome(t)
	if code, out, _ := runSS("trust", "list"); code != 0 || !strings.Contains(out, "Nothing is trusted") {
		t.Errorf("%d %q", code, out)
	}
	list := []policy.Trust{{Line: "rm build/x.o", Dir: "/work"}, {Line: "rm build/y.o", Dir: "/other"}, {Line: "echo \x1b[31mhi", Dir: "/w3"}}
	if err := config.SetTrusted(config.Path(), list); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runSS("trust", "list")
	if code != 0 || !strings.Contains(out, "1. rm build/x.o   (in /work)") || !strings.Contains(out, "2. rm build/y.o   (in /other)") || strings.Contains(out, "\x1b") || !strings.Contains(out, "trust remove N") {
		t.Errorf("%d %q", code, out)
	}
	if code, out, _ := runSS("trust", "remove", "1"); code != 0 || !strings.Contains(out, "ask again") {
		t.Errorf("%d %q", code, out)
	}
	if got := trustedNow(t); len(got) != 2 || got[0].Dir != "/other" {
		t.Errorf("%+v", got)
	}
	for _, n := range []string{"0", "3", "x", "-1"} {
		if code, _, errOut := runSS("trust", "remove", n); code != 1 || !strings.Contains(errOut, "not on the list") {
			t.Errorf("%s: %d %q", n, code, errOut)
		}
	}
	if code, _, _ := runSS("trust", "clear"); code != 0 {
		t.Error("clear failed")
	}
	if got := trustedNow(t); len(got) != 0 {
		t.Errorf("%+v", got)
	}
	for _, args := range [][]string{{"trust"}, {"trust", "list", "x"}, {"trust", "remove"}, {"trust", "add", "ls"}} {
		if code, _, errOut := runSS(args...); code != 2 || !strings.Contains(errOut, "safeshell trust list") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
}

func TestTrustCommandSaysWhenTheSettingsCannotBeUsedOrSaved(t *testing.T) {
	p := trustHome(t)
	os.MkdirAll(filepath.Dir(p), 0o700)
	os.WriteFile(p, []byte(`{"trusted":[{"line":"ls","dir":"app"}]}`), 0o600)
	// A bad entry is ignored, so the command that removes entries still works.
	if code, out, errOut := runSS("trust", "list"); code != 0 || strings.Contains(out, "app") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
	if code, _, errOut := runSS("trust", "clear"); code != 0 {
		t.Errorf("clear: %d %q", code, errOut)
	}
	os.WriteFile(p, []byte(`{"trusted":[`), 0o600) // a file that is not JSON is still an error, with the next step
	if code, _, errOut := runSS("trust", "list"); code != 1 || errOut == "" {
		t.Errorf("damaged: %d %q", code, errOut)
	}
	os.WriteFile(p, []byte(`{"trusted":[{"line":"ls","dir":"/app"}]}`), 0o600)
	if os.Getuid() == 0 {
		t.Skip("root can write anywhere")
	}
	os.Chmod(filepath.Dir(p), 0o500)
	t.Cleanup(func() { os.Chmod(filepath.Dir(p), 0o700) })
	if code, _, errOut := runSS("trust", "clear"); code != 1 || !strings.Contains(errOut, "cannot save") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestTrustHasHelp(t *testing.T) {
	if code, out, _ := runSS("trust", "--help"); code != 0 || !strings.Contains(out, "safeshell trust list") || !strings.Contains(out, "always run this exact command") {
		t.Errorf("%d %q", code, out)
	}
}

func TestATrustedLineIsNotHeldUpByAnAIProposal(t *testing.T) {
	trustHome(t)
	if err := config.SetTrusted(config.Path(), []policy.Trust{{Line: "rm build/x.o", Dir: "/work"}}); err != nil {
		t.Fatal(err)
	}
	a := r2Sample()
	a.Verdict.Alternatives = nil // nothing from the rules, so an untrusted line would ask the AI for one
	fakeAnalyze(t, a)
	asked := useVerified(t, engine.Analysis{})
	useAI(t, fakeAI{reply: `{"level":"R2","reason":"Deletes one build file.","command":"rm -i build/x.o","note":"Asks first."}`}, nil)
	if code, out, _ := runSS("guard", "--cwd", "/work", "--", "rm build/x.o"); code != 0 || out != "rm build/x.o\n" || *asked != "" {
		t.Errorf("code %d out %q; proposal checked: %q", code, out, *asked)
	}
}

func TestTrustIsNotSavedWithoutAFullFolderPath(t *testing.T) {
	trustHome(t)
	var errOut strings.Builder
	saveTrust(policy.NewTrust("rm build/x.o", ""), &errOut)
	if got := trustedNow(t); len(got) != 0 || !strings.Contains(errOut.String(), "ask again") {
		t.Errorf("%+v %q", got, errOut.String())
	}
}
