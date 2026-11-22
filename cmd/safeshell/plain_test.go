package main

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run: go test ./cmd/safeshell -run Plain -update):\n%s", path, got)
	}
}

// plainMode turns plain output on for one test and restores it afterwards.
func plainMode(t *testing.T) {
	t.Helper()
	t.Setenv("SAFESHELL_PLAIN", "1")
}

// screenReaderHostile matches what plain output must not contain: colour codes,
// box drawing, and runs of spaces that line up columns.
var screenReaderHostile = regexp.MustCompile("\x1b|[─-╿]|\\S {2,}\\S")

// plainCases are the decisions SafeShell can reach, with the answer typed at the prompt.
func plainCases() map[string]struct {
	a      engine.Analysis
	answer string
} {
	allow := sampleAnalysis()
	allow.Level, allow.Decision = domain.R0, domain.Allow
	allow.Verdict = domain.Verdict{Level: domain.R0, Findings: []domain.Finding{{Level: domain.R0, Reason: "Only reads."}}}
	allow.Effects = nil
	confirm := sampleAnalysis()
	confirm.Level, confirm.Decision = domain.R2, domain.Confirm
	confirm.Line = "rm notes.txt"
	confirm.Verdict = domain.Verdict{Level: domain.R2, Findings: []domain.Finding{{Level: domain.R2, Reason: "Deletes a file."}}}
	confirm.Effects = nil
	hard := sampleAnalysis()
	hard.Line = "rm -rf /"
	hard.Level, hard.Decision = domain.R4, domain.HardBlock
	hard.Verdict = domain.Verdict{Level: domain.R4, Findings: []domain.Finding{{Level: domain.R4, Reason: "Erases the whole system"}}}
	hard.Effects = nil
	team := sampleAnalysis()
	team.Team.Block = "production data is off limits"
	return map[string]struct {
		a      engine.Analysis
		answer string
	}{
		"allow":                  {allow, ""},
		"confirm":                {confirm, "\n"},
		"block_with_safer":       {sampleAnalysis(), "c\n"},
		"hard_block":             {hard, "\n"},
		"team_policy_block":      {team, ""},
		"block_do_it_anyway_yes": {sampleAnalysis(), "d\nyes\n"},
	}
}

func TestPlainGoldenForEveryDecision(t *testing.T) {
	plainMode(t)
	for name, tc := range plainCases() {
		t.Run(name, func(t *testing.T) {
			fakeAnalyze(t, tc.a)
			_, out, _ := runSS("analyze", "--cwd", "/work", "x")
			golden(t, "plain_"+name+"_analyze", out)
			answer(t, tc.answer)
			_, _, errOut := runSS("guard", "--cwd", "/work", "--", tc.a.Line)
			golden(t, "plain_"+name+"_guard", errOut)
			for _, got := range []string{out, errOut} {
				if screenReaderHostile.MatchString(got) {
					t.Errorf("plain output has colour, box drawing or aligned columns:\n%s", got)
				}
			}
		})
	}
}

func TestPlainOutputIsOneSentencePerLine(t *testing.T) {
	plainMode(t)
	fakeAnalyze(t, sampleAnalysis())
	_, out, _ := runSS("analyze", "x")
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if !strings.HasSuffix(l, ".") && !strings.HasPrefix(l, "The command is: ") && !strings.HasPrefix(l, "Safer option ") { // a command is shown exactly, with no full stop added
			t.Errorf("line is not a full sentence: %q", l)
		}
	}
}

func TestPlainAnswersAreTheSameAsDefault(t *testing.T) {
	for _, tc := range []struct {
		in   string
		code int
		out  string
	}{
		{"c\n", 1, ""}, {"\n", 1, ""}, {"1\n", 0, "trash-put ~/projects\n"},
		{"d\nyes\n", 0, "rm -rf ~/projects\n"}, {"d\nno\n", 1, ""}, {"9\nc\n", 1, ""},
	} {
		for _, plain := range []string{"", "1"} {
			t.Setenv("SAFESHELL_PLAIN", plain)
			fakeAnalyze(t, sampleAnalysis())
			answer(t, tc.in)
			if code, out, _ := runSS("guard", "--", "rm -rf ~/projects"); code != tc.code || out != tc.out {
				t.Errorf("plain=%q input %q: code %d out %q", plain, tc.in, code, out)
			}
		}
	}
}

func TestPlainProceedForACommandTheAssistantWrote(t *testing.T) {
	plainMode(t)
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	a.Verdict.Level = domain.R0
	fakeAnalyze(t, a)
	answer(t, "\n")
	var b strings.Builder
	if code := guardLine("ls", "/work", &strings.Builder{}, &b, false, fromAssistant); code != 0 {
		t.Errorf("code %d", code)
	}
	if !strings.HasSuffix(b.String(), "Do you want to run it? ") || strings.Contains(b.String(), "[Y/n]") {
		t.Errorf("%q", b.String())
	}
}

func TestPlainCheckAndScripts(t *testing.T) {
	plainMode(t)
	code, out, _ := runSS("check", "rm -rf ~/projects")
	if code != 3 {
		t.Errorf("exit %d", code)
	}
	golden(t, "plain_check_block", out)
	_, out, _ = runSS("check", "mytool --go; ls")
	golden(t, "plain_check_unclassified", out)
	_, out, _ = runSS("check", "echo hi | sudo rm -rf x")
	golden(t, "plain_check_part", out)
	d := t.TempDir()
	good, bad := writeNamed(t, d, "good.sh", goodScript), writeNamed(t, d, "bad.sh", badScript)
	_, out, _ = runSS("check", "--script", good, bad)
	golden(t, "plain_check_script", strings.ReplaceAll(out, d, "DIR"))

	fakeAnalyze(t, sampleAnalysis())
	_, out, _ = runSS("analyze", "--cwd", "/work", "--script", bad)
	golden(t, "plain_analyze_script", out)
	team := sampleAnalysis()
	team.Team.Block = "no scripts here"
	fakeAnalyze(t, team)
	_, out, _ = runSS("analyze", "--cwd", "/work", "--script", good)
	golden(t, "plain_analyze_script_team", out)
}

func TestPlainSandboxNotesAndQuietFindings(t *testing.T) {
	plainMode(t)
	a := sampleAnalysis()
	a.Verdict.Findings = append([]domain.Finding{{Level: domain.R0, Reason: "Reads a file."}}, a.Verdict.Findings...)
	a.Sim = &sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "/boot is partial"}}}
	fakeAnalyze(t, a)
	_, out, _ := runSS("analyze", "--cwd", "/boot", "x")
	if !strings.Contains(out, "Sandbox note: /boot is partial.\n") || strings.Contains(out, "Reads a file") {
		t.Errorf("%s", out)
	}
	_, out, _ = runSS("analyze", "--cwd", "/tmp", "x")
	if !strings.Contains(out, "1 sandbox note about other folders was hidden; add --verbose to see it.\n") {
		t.Errorf("%s", out)
	}
}

func TestPlainWhenTheCommandAloneLookedSafer(t *testing.T) {
	plainMode(t)
	a := sampleAnalysis()
	a.Verdict.Level = domain.R1
	a.Host.Reason = "it ran on a production host"
	a.SimNote = "The sandbox could not run"
	fakeAnalyze(t, a)
	_, out, _ := runSS("analyze", "x")
	for _, want := range []string{
		"The command text alone looked like risk level 1 of 4; what it would really do raised it to level 3 of 4.",
		"Raised because it ran on a production host.", "The sandbox could not run.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	d := t.TempDir()
	r2 := writeNamed(t, d, "r2.sh", r2Script)
	_, out, _ = runSS("analyze", "--script", r2)
	if !strings.Contains(out, "The lines alone looked like risk level 2 of 4; what the script would really do raised it to level 3 of 4.") {
		t.Errorf("%s", out)
	}
}

func TestPlainFlagTurnsItOnForCheckAnalyzeAndGuard(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--plain", "ls"}, {"check", "--json", "--plain", "ls"}, {"analyze", "--cwd", "/work", "--plain", "ls"},
		{"guard", "--plain", "--cwd", "/work", "--", "ls"},
	} {
		t.Setenv("SAFESHELL_PLAIN", "") // restores the variable after the test
		fakeAnalyze(t, sampleAnalysis())
		answer(t, "c\n")
		runSS(args...)
		if os.Getenv("SAFESHELL_PLAIN") != "1" {
			t.Errorf("%v: --plain was not taken", args)
		}
	}
	// A --plain after the command line belongs to the command, as with --json.
	t.Setenv("SAFESHELL_PLAIN", "")
	runSS("check", "ls", "--plain")
	if os.Getenv("SAFESHELL_PLAIN") != "" {
		t.Error("--plain after the command line must not be read")
	}
	if got := takePlain([]string{"--cwd", "--plain", "ls"}); len(got) != 3 {
		t.Errorf("--plain as the value of --cwd was read as an option: %v", got)
	}
}

func TestPlainFollowsTheSettingsFile(t *testing.T) {
	t.Setenv("SAFESHELL_PLAIN", "")
	if plainOn() {
		t.Fatal("plain must be off by default")
	}
	old := plainSetting
	t.Cleanup(func() { plainSetting = old })
	plainSetting = readPlainSetting
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if plainOn() {
		t.Error("a missing settings file means off")
	}
	_ = os.MkdirAll(filepath.Join(cfg, "safeshell"), 0o700)
	path := filepath.Join(cfg, "safeshell", "config.json")
	_ = os.WriteFile(path, []byte(`{"plain": true}`), 0o600)
	if !plainOn() {
		t.Error(`"plain": true must turn it on`)
	}
	_ = os.WriteFile(path, []byte(`{not json`), 0o600)
	if plainOn() {
		t.Error("a damaged settings file means off")
	}
}
