package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

const esc = "\x1b[2K"

func noRawEscape(t *testing.T, what, s string) {
	t.Helper()
	if strings.ContainsRune(s, 0x1b) {
		t.Errorf("%s printed a raw escape byte: %q", what, s)
	}
}

func TestSimulatePrintsUntrustedTextEscaped(t *testing.T) {
	fakeSim(t, sandbox.Result{
		Stdout: "out" + esc + "\n", Stderr: "err\x1b]52;c;eA==\x07\n",
		Diff:     sandbox.FileDiff{Changes: []sandbox.Change{{Path: "tmp/a" + esc + "b", Kind: sandbox.Created, Files: 1}}},
		Warnings: []sandbox.Warning{{Paths: []string{"/work/x"}, Text: "/work/x" + esc + " is not available"}},
	}, nil)
	_, out, _ := runSS("simulate", "--cwd", "/work", "--verbose", "echo"+esc)
	noRawEscape(t, "simulate", out)
	for _, want := range []string{`/tmp/a\x1b[2Kb`, `out\x1b[2K`, `err\x1b]52;c;eA==\x07`, `$ echo\x1b[2K`, `x\x1b[2K is not`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyzeAndCheckPrintUntrustedTextEscaped(t *testing.T) {
	a := sampleAnalysis()
	a.Line = "rm x" + esc
	a.Verdict.Findings[0].Reason = "bad" + esc
	a.Effects[0].Summary = "sum" + esc
	a.Effects[0].Details = []string{"deleted /home/me/a" + esc + "b"}
	a.SimNote = "note" + esc
	a.Verdict.Alternatives[0] = domain.Alternative{Command: "trash" + esc, Note: "moves" + esc}
	a.Sim = &sandbox.Result{Warnings: []sandbox.Warning{{Text: "warn" + esc}}}
	fakeAnalyze(t, a)
	_, out, _ := runSS("analyze", "--verbose", "x")
	noRawEscape(t, "analyze", out)
	for _, want := range []string{`rm x\x1b[2K`, `a\x1b[2Kb`, `sum\x1b[2K`, `note\x1b[2K`, `trash\x1b[2K`, `moves\x1b[2K`, `warn\x1b[2K`, `bad\x1b[2K`} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	_, out, _ = runSS("check", "rm -rf ~/a"+esc+"; echo 'x"+esc+"'")
	noRawEscape(t, "check", out)
	if !strings.Contains(out, `\x1b[2K`) {
		t.Errorf("check output lost the escape marker:\n%s", out)
	}
	_, out, _ = runSS("check", "zzunknowncmd"+esc+" --x")
	noRawEscape(t, "check unclassified", out)
	if !strings.Contains(out, "Not recognised yet: zzunknowncmd\\x1b[2K") {
		t.Errorf("unclassified command not escaped:\n%s", out)
	}
}

func TestAskRefusesCommandsWithControlCharacters(t *testing.T) {
	useAI(t, fakeAI{reply: `{"command":"ls\u001b[2K; rm x","note":""}`}, nil)
	code, out, errOut := runSS("ask", "show files")
	if code != 1 || out != "" || strings.Contains(errOut, "Proposed") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestAskPrintsAIWordsSafely(t *testing.T) {
	allowAnalysis(t)
	useAI(t, fakeAI{reply: `{"command":"ls","note":"safe\u202e"}`}, nil)
	answer(t, "n\n")
	_, _, errOut := runSS("ask", "show files")
	if !strings.Contains(errOut, "safe") || strings.ContainsAny(errOut, "\u202e") {
		t.Errorf("note not made safe: %q", errOut)
	}
	useAI(t, fakeAI{reply: `{"answer":"a\u009b"}`}, nil)
	_, _, errOut = runSS("ask", "what is x")
	if !strings.HasPrefix(errOut, "a") || strings.ContainsAny(errOut, "\u009b") {
		t.Errorf("answer not made safe: %q", errOut)
	}
	useAI(t, fakeAI{reply: "text\u009b"}, nil)
	_, out, _ := runSS("explain", "ls")
	if !strings.Contains(out, "text") || strings.ContainsAny(out, "\u009b") {
		t.Errorf("explanation not made safe: %q", out)
	}
}

func TestAskQuestionsAreMadeSafe(t *testing.T) {
	var calls []string
	useAI(t, seqAI{replies: []string{`{"question":"which\u202e?"}`}, calls: &calls}, nil)
	typed(t, "")
	_, _, errOut := runSS("ask", "open the config")
	if !strings.Contains(errOut, "which") || strings.ContainsAny(errOut, "\u202e") {
		t.Errorf("question not made safe: %q", errOut)
	}
	pr, pw, err := os.Pipe() // not a terminal: the question is shown, not asked
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	guardInput = func() io.Reader { return pr }
	calls = nil
	useAI(t, seqAI{replies: []string{`{"question":"which\u202e?"}`}, calls: &calls}, nil)
	_, _, errOut = runSS("ask", "open the config")
	if !strings.Contains(errOut, "which") || strings.ContainsAny(errOut, "\u202e") {
		t.Errorf("question not made safe: %q", errOut)
	}
}

func TestProposalThatChangesTheShellDefaultsToNo(t *testing.T) {
	allowAnalysis(t)
	useAI(t, fakeAI{reply: `{"command":"alias ls='rm -rf'"}`}, nil)
	answer(t, "\n")
	code, out, errOut := runSS("ask", "make an alias")
	if code != 1 || out != "" || !strings.Contains(errOut, "[y/N]") {
		t.Errorf("Enter must not run a shell-changing line: %d %q %q", code, out, errOut)
	}
	for line, want := range map[string]bool{"export A=1": true, "ls; eval $x": true, "(trap '' INT)": true, "function f { :; }": true, "echo exported": false, "ls -la": false} {
		if changesShell.MatchString(line) != want {
			t.Errorf("%q: want %v", line, want)
		}
	}
}

func TestSnapshotsAndUndoPrintTheCommandEscaped(t *testing.T) {
	useStore(t)
	a := sampleAnalysis()
	a.Line = "rm x" + esc
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: "nonexistent/dir/f", Kind: sandbox.Created}}}}
	fakeAnalyze(t, a)
	answer(t, "d\nyes\n")
	runSS("guard", "--", "rm x")
	_, out, _ := runSS("snapshots")
	noRawEscape(t, "snapshots", out)
	if !strings.Contains(out, `rm x\x1b[2K`) {
		t.Errorf("snapshots: %q", out)
	}
	_, out, _ = runSS("undo")
	noRawEscape(t, "undo", out)
	if !strings.Contains(out, `Undoing: rm x\x1b[2K`) {
		t.Errorf("undo: %q", out)
	}
}
