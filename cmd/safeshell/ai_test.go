package main

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

type fakeAI struct {
	reply string
	err   error
}

func (f fakeAI) Name() string { return "fake" }
func (f fakeAI) Complete(context.Context, string, string) (string, error) {
	return f.reply, f.err
}

func useAI(t *testing.T, b ai.Backend, err error) {
	t.Helper()
	old := backend
	backend = func() (ai.Backend, error) { return b, err }
	t.Cleanup(func() { backend = old })
}

func TestGuardAIReviewCanRaiseAConfirmIntoABlock(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R2, domain.Confirm
	a.Verdict.Level = domain.R2
	fakeAnalyze(t, a)
	useAI(t, fakeAI{reply: `{"level":"R3","reason":"Deletes the only copy of a thesis."}`}, nil)
	answer(t, "\n")
	code, _, errOut := runSS("guard", "--", "mv thesis.pdf /dev/null")
	if code != 1 || !strings.Contains(errOut, "BLOCK") || !strings.Contains(errOut, "only copy of a thesis") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestGuardAIFailureNeverBlocksOrLowers(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R2, domain.Confirm
	a.Verdict.Level = domain.R2
	fakeAnalyze(t, a)
	useAI(t, fakeAI{err: errors.New("the local engine stopped while starting")}, nil)
	answer(t, "y\n")
	code, out, errOut := runSS("guard", "--", "touch x")
	if code != 0 || out != "touch x\n" || !strings.Contains(errOut, "AI review unavailable") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
	useAI(t, fakeAI{reply: `{"level":"R0","reason":"harmless"}`}, nil)
	answer(t, "y\n")
	if _, _, e := runSS("guard", "--", "touch x"); !strings.Contains(e, "CONFIRM") {
		t.Errorf("the model lowered the level: %q", e)
	}
}

func TestExplain(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	useAI(t, fakeAI{reply: "It deletes your projects folder for good."}, nil)
	code, out, _ := runSS("explain", "rm", "-rf", "~/projects")
	if code != 0 || !strings.Contains(out, "In plain English") || !strings.Contains(out, "for good") {
		t.Errorf("%d %q", code, out)
	}
	useAI(t, nil, nil)
	if code, _, e := runSS("explain", "ls"); code != 1 || !strings.Contains(e, "AI is off") {
		t.Errorf("%d %q", code, e)
	}
}

func TestAskTranslatesThenGuards(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	got := fakeAnalyze(t, a)
	useAI(t, fakeAI{reply: `{"command":"ls -la","note":"lists everything"}`}, nil)
	answer(t, "\n") // Enter accepts the proposal
	code, out, errOut := runSS("ask", "show", "me", "all", "files")
	if code != 0 || out != "ls -la\n" || !strings.Contains(errOut, "Proposed: ls -la") || got[0] != "ls -la" {
		t.Errorf("%d %q %q %v", code, out, errOut, *got)
	}
}

func TestAskDangerousProposalIsStillBlocked(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	useAI(t, fakeAI{reply: `{"command":"rm -rf ~/projects"}`}, nil)
	answer(t, "\n")
	code, out, _ := runSS("ask", "clean up")
	if code != 1 || out != "" {
		t.Errorf("%d %q", code, out)
	}
}

func TestAskErrors(t *testing.T) {
	useAI(t, fakeAI{reply: `nonsense`}, nil)
	if code, _, _ := runSS("ask", "x"); code != 1 {
		t.Error("bad model output")
	}
	useAI(t, nil, errors.New("bad config"))
	if code, _, _ := runSS("ask", "x"); code != 1 {
		t.Error("config error")
	}
	if code, _, _ := runSS("ask"); code != 2 {
		t.Error("usage")
	}
	if code, _, _ := runSS("explain"); code != 2 {
		t.Error("usage")
	}
}

func TestAskAnswersQuestionsWithoutRunningAnything(t *testing.T) {
	useAI(t, fakeAI{reply: `{"command":"","answer":"Owner can do everything."}`}, nil)
	code, out, errOut := runSS("ask", "what is chmod 755")
	if code != 1 || out != "" || !strings.Contains(errOut, "Owner can do everything") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestAskDeclinedProposalRunsNothing(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	useAI(t, fakeAI{reply: `{"command":"ls"}`}, nil)
	answer(t, "n\n")
	if code, out, _ := runSS("ask", "list"); code != 1 || out != "" {
		t.Errorf("%d %q", code, out)
	}
}

func TestGuardTreatsUnknownEnglishAsARequest(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	got := fakeAnalyze(t, a)
	useAI(t, fakeAI{reply: `{"command":"du -ah . | sort -rh | head","note":"biggest first"}`}, nil)
	answer(t, "\n")
	code, out, _ := runSS("guard", "--unknown", "--", "show me the biggest files")
	if code != 0 || out != "du -ah . | sort -rh | head\n" || got[0] != "du -ah . | sort -rh | head" {
		t.Errorf("%d %q %v", code, out, *got)
	}
}

func TestGuardKnownCommandsNeverGoToTheModel(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	useAI(t, fakeAI{err: errors.New("must not be called")}, nil)
	if code, out, _ := runSS("guard", "--", "ls files here now"); code != 0 || out != "ls files here now\n" {
		t.Errorf("%d %q", code, out)
	}
}

func TestGuardLetsEnglishFallThroughWhenAIIsOff(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	useAI(t, nil, nil)
	code, out, _ := runSS("guard", "--unknown", "--", "gti status now")
	if code != 0 || out != "gti status now\n" {
		t.Errorf("implicit English with AI off must reach the shell unchanged: %d %q", code, out)
	}
	if code, out, _ := runSS("guard", "--unknown", "--", "? list files"); code != 1 || out != "" {
		t.Errorf("an explicit ? with AI off must say so, not run: %d %q", code, out)
	}
}

func TestAskTurnsAnUnrunnableProposalIntoAdvice(t *testing.T) {
	useAI(t, fakeAI{reply: `{"command":":q","note":"quits vim"}`}, nil)
	code, out, errOut := runSS("ask", "how do i exit vim")
	if code != 1 || out != "" || !strings.Contains(errOut, ":q") || strings.Contains(errOut, "Run it?") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestAIReviewSkipsCommandsThatNeedNoDecision(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R1, domain.Allow
	a.Verdict.Level = domain.R1
	fakeAnalyze(t, a)
	useAI(t, fakeAI{err: errors.New("must not be called for an R1 command")}, nil)
	if code, _, errOut := runSS("guard", "--", "mkdir x"); code != 0 || strings.Contains(errOut, "AI review") {
		t.Errorf("%d %q", code, errOut)
	}
}

type seqAI struct {
	replies []string
	calls   *[]string
}

func (s seqAI) Name() string { return "seq" }
func (s seqAI) Complete(_ context.Context, _, user string) (string, error) {
	*s.calls = append(*s.calls, user)
	return s.replies[len(*s.calls)-1], nil
}

const askWhich = `{"question":"2 files match config*: config.txt, config.yaml. Which one?"}`

// typed makes every read of the prompt input continue from the same text, like one terminal.
func typed(t *testing.T, s string) {
	t.Helper()
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	r := strings.NewReader(s)
	guardInput = func() io.Reader { return r }
}

func TestAskAsksAQuestionThenProposesAndGuards(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	var calls []string
	useAI(t, seqAI{replies: []string{askWhich, `{"command":"cat config.yaml"}`}, calls: &calls}, nil)
	typed(t, "the yaml one\n\n")
	code, out, errOut := runSS("ask", "open", "the", "config")
	if code != 0 || out != "cat config.yaml\n" || !strings.Contains(errOut, "Which one?") || !strings.Contains(errOut, "Proposed: cat config.yaml") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
	if len(calls) != 2 || !strings.Contains(calls[1], "clarification 1: the yaml one") {
		t.Errorf("%q", calls)
	}
}

func TestAskEmptyAnswerCancels(t *testing.T) {
	var calls []string
	useAI(t, seqAI{replies: []string{askWhich}, calls: &calls}, nil)
	typed(t, "\n")
	code, out, errOut := runSS("ask", "open the config")
	if code != 1 || out != "" || !strings.Contains(errOut, "Cancelled. Nothing was run.") || len(calls) != 1 {
		t.Errorf("%d %q %q %d", code, out, errOut, len(calls))
	}
	calls = nil
	typed(t, "") // end of input
	if code, _, e := runSS("ask", "open the config"); code != 1 || !strings.Contains(e, "Cancelled") {
		t.Errorf("%d %q", code, e)
	}
}

func TestAskWithoutATerminalPrintsTheQuestion(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer pr.Close()
	defer pw.Close()
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	guardInput = func() io.Reader { return pr }
	var calls []string
	useAI(t, seqAI{replies: []string{askWhich}, calls: &calls}, nil)
	code, out, errOut := runSS("ask", "open the config")
	if code != 1 || out != "" || !strings.Contains(errOut, "Which one?") || len(calls) != 1 {
		t.Errorf("%d %q %q %d", code, out, errOut, len(calls))
	}
}

func TestAskGivesUpAfterTwoQuestions(t *testing.T) {
	var calls []string
	useAI(t, seqAI{replies: []string{askWhich, askWhich, askWhich}, calls: &calls}, nil)
	typed(t, "one\ntwo\nthree\n")
	code, out, errOut := runSS("ask", "open the config")
	if code != 1 || out != "" || len(calls) != 3 || strings.Contains(errOut, "Cancelled") {
		t.Errorf("%d %q %q %d", code, out, errOut, len(calls))
	}
}

func TestReadLine(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"lf", "yes\nnext", "yes"},
		{"crlf", "yes\r\n", "yes"},
		{"eof without newline", "yes", "yes"},
		{"empty", "\n", ""},
		{"capped", strings.Repeat("a", 3*maxReply), strings.Repeat("a", maxReply)},
	} {
		if got, err := readLine(strings.NewReader(tc.in)); err != nil || got != tc.want {
			t.Errorf("%s: %q %v", tc.name, got, err)
		}
	}
	if _, err := readLine(strings.NewReader("")); err == nil {
		t.Error("EOF with nothing typed must be an error")
	}
}
