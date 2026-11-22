package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// stepAI answers with the next reply each time it is asked and keeps every prompt it was shown.
type stepAI struct {
	name    string
	replies []string
	err     error
	prompts []string
}

func (s *stepAI) Name() string { return s.name }
func (s *stepAI) Complete(_ context.Context, _, user string) (string, error) {
	s.prompts = append(s.prompts, user)
	if s.err != nil {
		return "", s.err
	}
	if len(s.prompts) > len(s.replies) {
		return `{"command":"","answer":"out of replies"}`, nil
	}
	return s.replies[len(s.prompts)-1], nil
}

func cmdReply(c string) string { return fmt.Sprintf(`{"command":%q,"note":"a step"}`, c) }

// fakeRun replaces runCommand: it records what ran and prints out for every command.
func fakeRun(t *testing.T, out string, err error) *[]string {
	t.Helper()
	old := runCommand
	t.Cleanup(func() { runCommand = old })
	var ran []string
	runCommand = func(cmd, _ string, stdout, _ io.Writer) error {
		ran = append(ran, cmd)
		io.WriteString(stdout, out)
		return err
	}
	return &ran
}

func doWith(t *testing.T, sai *stepAI, level domain.Level, typed string) (code int, errOut string) {
	t.Helper()
	fakeAnalyze(t, proposal(level, "cat notes.txt"))
	autorunSet(t, false)
	useAI(t, sai, nil)
	answer(t, typed)
	code, _, errOut = runSS("do", "read", "my", "notes")
	return code, errOut
}

func TestDoRunsAStepAndShowsTheModelWhatItPrinted(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), `{"command":"","answer":"The notes say hello."}`}}
	ran := fakeRun(t, "hello from the notes\nAPI_KEY=abc123\n", nil)
	code, errOut := doWith(t, sai, domain.R0, "y\n")
	if code != 0 || len(*ran) != 1 || (*ran)[0] != "cat notes.txt" {
		t.Fatalf("%d %v\n%s", code, *ran, errOut)
	}
	if !strings.Contains(errOut, "Run it?") || !strings.Contains(errOut, "Done: The notes say hello.") {
		t.Errorf("it must ask first and report the end:\n%s", errOut)
	}
	second := sai.prompts[1]
	if !strings.Contains(second, "command: cat notes.txt") || !strings.Contains(second, "hello from the notes") {
		t.Errorf("the model must see what the command printed:\n%s", second)
	}
	if strings.Contains(second, "abc123") || !strings.Contains(second, "API_KEY=***") {
		t.Errorf("secrets must be hidden from the model:\n%s", second)
	}
}

func TestDoStopsWhenThePersonSaysNo(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), cmdReply("ls")}}
	ran := fakeRun(t, "", nil)
	code, errOut := doWith(t, sai, domain.R0, "n\n")
	if code != 1 || len(*ran) != 0 || len(sai.prompts) != 1 {
		t.Errorf("no must end the task without running anything: %d %v %d\n%s", code, *ran, len(sai.prompts), errOut)
	}
}

func TestDoAsksAboutARiskyStepAndRunsItOnYes(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("chmod -R 755 projects"), `{"command":"","answer":"done"}`}}
	ran := fakeRun(t, "", nil)
	a := proposal(domain.R2, "chmod -R 755 projects")
	a.Decision = domain.Confirm
	fakeAnalyze(t, a)
	autorunSet(t, true) // autorun never covers an R2 step
	useAI(t, sai, nil)
	answer(t, "y\n")
	code, _, errOut := runSS("do", "open", "up", "projects")
	if code != 0 || len(*ran) != 1 || strings.Contains(errOut, "Running it now") || !strings.Contains(errOut, "Run it anyway?") {
		t.Errorf("a risky step must ask, then run on yes: %d %v\n%s", code, *ran, errOut)
	}
}

func TestDoAsksEvenWhenAutorunIsOn(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), `{"command":"","answer":"done"}`}}
	ran := fakeRun(t, "x", nil)
	fakeAnalyze(t, proposal(domain.R0, "cat notes.txt"))
	autorunSet(t, true)
	useAI(t, sai, nil)
	answer(t, "n\n")
	autorunHome(t)
	code, _, errOut := runSS("do", "read", "my", "notes")
	if code != 1 || len(*ran) != 0 || !strings.Contains(errOut, "Run it?") || strings.Contains(errOut, "Running it now") {
		t.Errorf("the model reads files in a task, so every step must ask: %d %v\n%s", code, *ran, errOut)
	}
}

func TestDoStopsOnABlockedStep(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("rm -rf projects"), cmdReply("ls")}}
	ran := fakeRun(t, "", nil)
	a := sampleAnalysis() // R3, blocked
	fakeAnalyze(t, a)
	autorunSet(t, true)
	useAI(t, sai, nil)
	answer(t, "c\n")
	code, _, errOut := runSS("do", "clean", "up")
	if code != 1 || len(*ran) != 0 || strings.Contains(errOut, "Proposed: ls") {
		t.Errorf("a blocked step must end the task: %d %v\n%s", code, *ran, errOut)
	}
}

func TestDoSkipsAStepThatWouldChangeTheShell(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("export FOO=1"), `{"command":"","answer":"done"}`}}
	ran := fakeRun(t, "", nil)
	code, errOut := doWith(t, sai, domain.R1, "")
	if code != 0 || len(*ran) != 0 || !strings.Contains(errOut, "Skipped: export FOO=1") || !strings.Contains(sai.prompts[1], "not run: the agent cannot change your shell") {
		t.Errorf("%d %v\n%s\n%s", code, *ran, errOut, sai.prompts[1])
	}
}

func TestDoTellsTheModelWhenAStepFailed(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), `{"command":"","answer":"it failed"}`}}
	fakeRun(t, "cat: notes.txt: No such file", errors.New("exit status 1"))
	if code, _ := doWith(t, sai, domain.R0, "y\n"); code != 0 || !strings.Contains(sai.prompts[1], "failed (exit status 1)") || !strings.Contains(sai.prompts[1], "No such file") {
		t.Errorf("%d\n%s", code, sai.prompts[1])
	}
}

func TestDoKeepsOnlyTheStartOfALongOutput(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), `{"command":"","answer":"done"}`}}
	old := runCommand
	t.Cleanup(func() { runCommand = old })
	runCommand = func(_, _ string, stdout, _ io.Writer) error {
		io.WriteString(stdout, strings.Repeat("a", maxStepOutput))
		io.WriteString(stdout, "THE-END")
		return nil
	}
	if code, _ := doWith(t, sai, domain.R0, "y\n"); code != 0 || strings.Contains(sai.prompts[1], "THE-END") {
		t.Errorf("%d: output past the cap must be dropped", code)
	}
}

func TestDoStopsWhenTheAssistantRepeatsItself(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), cmdReply("cat notes.txt")}}
	ran := fakeRun(t, "x", nil)
	code, errOut := doWith(t, sai, domain.R0, "y\ny\n")
	if code != 1 || len(*ran) != 1 || !strings.Contains(errOut, "repeated a command") {
		t.Errorf("%d %v\n%s", code, *ran, errOut)
	}
}

func TestDoStopsAfterEightSteps(t *testing.T) {
	var replies []string
	for i := range 12 {
		replies = append(replies, cmdReply(fmt.Sprintf("cat file%d", i)))
	}
	sai := &stepAI{name: "local:test", replies: replies}
	ran := fakeRun(t, "x", nil)
	code, errOut := doWith(t, sai, domain.R0, strings.Repeat("y\n", 12))
	if code != 1 || len(*ran) != maxDoSteps || !strings.Contains(errOut, "Stopped after 8 steps") {
		t.Errorf("%d %d\n%s", code, len(*ran), errOut)
	}
}

func TestDoEndings(t *testing.T) {
	for name, c := range map[string]struct {
		reply, want string
		code        int
	}{
		"a question":        {`{"question":"Which notes file?"}`, "The AI asks: Which notes file?", 1},
		"nothing to run":    {`{"note":"nothing"}`, "Done: the assistant had nothing more to run.", 0},
		"an answer at once": {`{"answer":"Nothing to do."}`, "Done: Nothing to do.", 0},
	} {
		sai := &stepAI{name: "local:test", replies: []string{c.reply}}
		ran := fakeRun(t, "", nil)
		useAI(t, sai, nil)
		code, _, errOut := runSS("do", "read", "my", "notes")
		if code != c.code || !strings.Contains(errOut, c.want) || len(*ran) != 0 {
			t.Errorf("%s: %d %q", name, code, errOut)
		}
	}
}

func TestDoReportsAModelError(t *testing.T) {
	useAI(t, &stepAI{name: "local:test", err: errors.New("the engine stopped")}, nil)
	if code, _, errOut := runSS("do", "read", "my", "notes"); code != 1 || !strings.Contains(errOut, "safeshell do: ") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestDoNeedsALocalModelAndATask(t *testing.T) {
	useAI(t, &stepAI{name: "claude-sonnet"}, nil)
	if code, _, errOut := runSS("do", "read", "my", "notes"); code != 1 || !strings.Contains(errOut, "only works with a local model") {
		t.Errorf("a cloud model must be refused: %d %q", code, errOut)
	}
	useAI(t, nil, nil)
	if code, _, errOut := runSS("do", "read", "my", "notes"); code != 1 || !strings.Contains(errOut, "AI is off") {
		t.Errorf("%d %q", code, errOut)
	}
	if code, _, _ := runSS("do"); code != 2 {
		t.Errorf("a task is required: %d", code)
	}
	if code, _, _ := runSS("do", "--bogus"); code != 2 {
		t.Errorf("an unknown option is refused: %d", code)
	}
}

func TestDoTreatsEnterAsNo(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("cat notes.txt"), cmdReply("ls")}}
	ran := fakeRun(t, "", nil)
	if code, errOut := doWith(t, sai, domain.R0, "\n"); code != 1 || len(*ran) != 0 || !strings.Contains(errOut, "[y/N]") {
		t.Errorf("a stray Enter must not approve a step: %d %v\n%s", code, *ran, errOut)
	}
}

func TestDoRefusesAChainThatCouldFallBackToTheCloud(t *testing.T) {
	useAI(t, ai.Chain{&stepAI{name: "local:qwen"}, &stepAI{name: "gemini:flash"}}, nil)
	if code, _, errOut := runSS("do", "read", "my", "notes"); code != 1 || !strings.Contains(errOut, "only works with a local model") {
		t.Errorf("hybrid mode must be refused: %d %q", code, errOut)
	}
}

func TestDoDoesNotOfferAlwaysRunThisForAModelStep(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("chmod -R 755 projects")}}
	ran := fakeRun(t, "", nil)
	a := proposal(domain.R2, "chmod -R 755 projects")
	a.Decision = domain.Confirm
	fakeAnalyze(t, a)
	useAI(t, sai, nil)
	answer(t, "a\n")
	if code, _, _ := runSS("do", "open", "up", "projects"); code != 1 || len(*ran) != 0 {
		t.Errorf("'a' must not run or trust a model step: %d %v", code, *ran)
	}
}

func TestDoTellsTheModelAboutTheReplacementThePersonPicked(t *testing.T) {
	sai := &stepAI{name: "local:test", replies: []string{cmdReply("rm -rf projects"), `{"command":"","answer":"done"}`}}
	ran := fakeRun(t, "", nil)
	fakeAnalyze(t, sampleAnalysis()) // R3 with the safer option trash-put ~/projects
	autorunSet(t, false)
	useAI(t, sai, nil)
	answer(t, "1\n")
	code, _, errOut := runSS("do", "clean", "up")
	if code != 0 || len(*ran) != 1 || (*ran)[0] != "trash-put ~/projects" {
		t.Fatalf("the safer option must run: %d %v\n%s", code, *ran, errOut)
	}
	if last := sai.prompts[len(sai.prompts)-1]; !strings.Contains(last, "command: trash-put ~/projects") || strings.Contains(last, "command: rm -rf projects") {
		t.Errorf("the model must be told what really ran:\n%s", last)
	}
}

// Both of a command's streams are written from two goroutines: run with -race.
func TestCappedSurvivesACommandWritingToBothStreams(t *testing.T) {
	var out capped
	w := io.MultiWriter(&out)
	if err := runCommand("for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do echo out; echo err >&2; done", t.TempDir(), w, io.MultiWriter(&out)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.buf.String(), "out") || !strings.Contains(out.buf.String(), "err") {
		t.Errorf("%q", out.buf.String())
	}
}
