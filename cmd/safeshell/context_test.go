package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/convo"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// useConversations keeps this terminal's notes in a temporary folder.
func useConversations(t *testing.T) convo.Store {
	t.Helper()
	s := convo.Store{Dir: filepath.Join(t.TempDir(), "c"), Now: time.Now}
	old := conversations
	conversations = func() convo.Store { return s }
	t.Cleanup(func() { conversations = old })
	return s
}

func TestRecentCommandsShowWhatBecameOfEachOne(t *testing.T) {
	now := time.Now().UTC()
	useHistoryLog(t,
		audit.Entry{Time: now.Add(-time.Hour), Line: "too old", Outcome: "ran"},
		audit.Entry{Time: now.Add(-3 * time.Minute), Line: "ls", Decision: "allow", Outcome: "allowed"},
		audit.Entry{Time: now.Add(-2 * time.Minute), Line: "rm -rf x", Decision: "block", Outcome: "cancelled"},
		audit.Entry{Time: now.Add(-time.Minute), Line: "git push", Decision: "confirm", Outcome: "cancelled"},
		audit.Entry{Time: now, Line: "mv a b", Decision: "confirm", Outcome: "ran"},
	)
	got := recentCommands()
	want := []string{"ls ran", "rm -rf x blocked", "git push cancelled", "mv a b ran"}
	if len(got) != len(want) {
		t.Fatalf("%+v", got)
	}
	for i, r := range got {
		if r.Line+" "+r.Outcome != want[i] {
			t.Errorf("%d: %+v", i, r)
		}
	}
}

func TestGatherEnvIncludesProjectRecentAndConversation(t *testing.T) {
	s := useConversations(t)
	useHistoryLog(t, audit.Entry{Time: time.Now().UTC(), Line: "make build", Decision: "allow", Outcome: "allowed"})
	t.Setenv("SAFESHELL_HOOK", "4242")
	_ = s.Add("4242", nl.Turn{Request: "show files", Command: "ls", Ran: true})
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := gatherEnv(dir)
	if len(e.Project) != 1 || len(e.Recent) != 1 || len(e.Conversation) != 1 || e.Conversation[0].Command != "ls" {
		t.Errorf("%+v", e)
	}
}

func TestAskRemembersTheExchangeAndTheNextAskSeesIt(t *testing.T) {
	s := useConversations(t)
	t.Setenv("SAFESHELL_HOOK", "4242")
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	var calls []string
	useAI(t, seqAI{replies: []string{`{"command":"du -ah . | sort -rh | head"}`, `{"command":"","answer":"Nothing to undo."}`, `{"command":"ls"}`}, calls: &calls}, nil)

	answer(t, "\n") // accept the proposal
	if code, _, _ := runSS("ask", "show the biggest files"); code != 0 {
		t.Fatal("first ask")
	}
	if got := s.Load("4242"); len(got) != 1 || !got[0].Ran || got[0].Request != "show the biggest files" {
		t.Fatalf("%+v", got)
	}

	if code, _, _ := runSS("ask", "undo that"); code != 1 {
		t.Fatal("an answer runs nothing")
	}
	if !strings.Contains(calls[1], "<conversation>") || !strings.Contains(calls[1], "proposed and run: du -ah") {
		t.Errorf("the second ask must see the first: %s", calls[1])
	}

	answer(t, "n\n") // decline the proposal
	_, _, _ = runSS("ask", "list")
	got := s.Load("4242")
	if len(got) != 3 || got[1].Answer != "Nothing to undo." || got[2].Ran {
		t.Errorf("%+v", got)
	}
}

func TestAskWithoutTheHookRemembersNothing(t *testing.T) {
	s := useConversations(t)
	t.Setenv("SAFESHELL_HOOK", "")
	useAI(t, fakeAI{reply: `{"command":"","answer":"ok"}`}, nil)
	_, _, _ = runSS("ask", "what is chmod")
	if _, err := os.Stat(s.Dir); err == nil {
		t.Error("nothing should be written without a shell pid")
	}
}

func TestBenchReportsTheContextCategoriesApart(t *testing.T) {
	aiDirs(t)
	oldB := benchBackend
	t.Cleanup(func() { benchBackend = oldB })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) { return fakeAI{reply: `{"command":"ls"}`}, nil }
	code, out, _ := runSS("ai", "bench", "--pace", "0s")
	if code != 0 || !strings.Contains(out, "With context and follow-ups") {
		t.Fatalf("%d %q", code, out)
	}
	for _, c := range eval.Categories {
		if !strings.Contains(out, "  "+c) {
			t.Errorf("no line for %s in %q", c, out)
		}
	}
}

func TestBenchCountsUnreachableContextRequestsAndReportsABadContextFile(t *testing.T) {
	aiDirs(t)
	oldB, oldL, oldC := benchBackend, loadRequests, loadContextRequests
	t.Cleanup(func() { benchBackend, loadRequests, loadContextRequests = oldB, oldL, oldC })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) { return fakeAI{err: errors.New("down")}, nil }
	loadRequests = func() ([]eval.Request, error) { return nil, nil }
	code, out, _ := runSS("ai", "bench")
	if code != 1 || !strings.Contains(out, "never reached the model") {
		t.Errorf("%d %q", code, out)
	}
	loadContextRequests = func() ([]eval.Request, error) { return nil, errors.New("broken context list") }
	if code, _, e := runSS("ai", "bench"); code != 1 || !strings.Contains(e, "broken context list") {
		t.Errorf("%d %q", code, e)
	}
}
