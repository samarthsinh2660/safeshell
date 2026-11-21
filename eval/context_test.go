package eval

import (
	"context"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/nl"
)

func TestContextSetLoadsAndCoversEveryCategory(t *testing.T) {
	reqs, err := LoadContextRequests()
	if err != nil {
		t.Fatal(err)
	}
	count := map[string]int{}
	for _, r := range reqs {
		count[r.Category]++
		if !r.MustAsk && r.Must == nil {
			t.Errorf("%q checks nothing", r.Text)
		}
	}
	for _, c := range Categories {
		if count[c] < 4 {
			t.Errorf("category %s has %d requests", c, count[c])
		}
	}
	base, _ := LoadRequests()
	if len(base) != 68 {
		t.Errorf("the original set must stay at 68 requests, got %d", len(base))
	}
	for _, r := range base {
		if r.Category != "" || r.Conversation != nil {
			t.Errorf("%q must not carry context", r.Text)
		}
	}
}

func TestContextColumnsAreParsed(t *testing.T) {
	reqs, err := parse("t", "follow-up\tnow delete\trm\t-\tR3\tgit repository, branch main ;; Go module (go.mod)\tblocked: rm x ;; ran: ls\tlist tmp => ls *.tmp ;; and more => find .\n", 8)
	if err != nil || len(reqs) != 1 {
		t.Fatal(reqs, err)
	}
	r := reqs[0]
	if r.Category != "follow-up" || r.Text != "now delete" || len(r.Project) != 2 || r.Project[1] != "Go module (go.mod)" {
		t.Errorf("%+v", r)
	}
	if len(r.Recent) != 2 || r.Recent[0] != (nl.Recent{Line: "rm x", Outcome: "blocked"}) {
		t.Errorf("%+v", r.Recent)
	}
	if len(r.Conversation) != 2 || r.Conversation[1].Request != "and more" || r.Conversation[1].Command != "find ." || !r.Conversation[1].Ran {
		t.Errorf("%+v", r.Conversation)
	}
	reqs, _ = parse("t", "should-ask\tx\t-\t-\tASK\t-\t-\t-\n", 8)
	if !reqs[0].MustAsk || reqs[0].Project != nil || reqs[0].Recent != nil || reqs[0].Conversation != nil {
		t.Errorf("%+v", reqs[0])
	}
}

func TestContextFileErrorsNameTheLine(t *testing.T) {
	for name, line := range map[string]string{
		"fields":   "context\tx\t-\t-\tR0\n",
		"category": "bogus\tx\t-\t-\tR0\t-\t-\t-\n",
		"must":     "context\tx\t(\t-\tR0\t-\t-\t-\n",
		"mustnot":  "context\tx\t-\t(\tR0\t-\t-\t-\n",
		"level":    "context\tx\t-\t-\tR9\t-\t-\t-\n",
	} {
		if _, err := parse("c.tsv", line, 8); err == nil || !strings.Contains(err.Error(), "c.tsv line 1") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMustAskPassesOnlyOnAQuestion(t *testing.T) {
	r := Request{Text: "rename the file", MustAsk: true}
	if ok, _ := Score(r, nl.Result{Question: "Which file?"}, nil); !ok {
		t.Error("a question is the right answer")
	}
	ok, why := Score(r, nl.Result{Command: "mv a b"}, nil)
	if ok || !strings.Contains(why, "did not ask") {
		t.Errorf("%v %q", ok, why)
	}
}

// spy records the prompt of every call and answers with a fixed reply.
type spy struct{ prompts *[]string }

func (spy) Name() string { return "spy" }
func (s spy) Complete(_ context.Context, _, user string) (string, error) {
	*s.prompts = append(*s.prompts, user)
	return `{"command":"ls"}`, nil
}

func TestBenchSendsEachRequestsOwnContextAndTalliesCategories(t *testing.T) {
	var prompts []string
	reqs := []Request{
		{Text: "one", Category: "context", Must: nil, Project: []string{"Makefile targets: test"}, Recent: []nl.Recent{{Line: "git pull", Outcome: "ran"}}},
		{Text: "two", Category: "follow-up", Conversation: []nl.Turn{{Request: "before", Command: "ls", Ran: true}}},
		{Text: "three", Category: "should-ask", MustAsk: true},
		{Text: "four"},
	}
	res := RunBench(context.Background(), spy{&prompts}, BenchEnv(), reqs, 0, nil)
	if !strings.Contains(prompts[0], "project: Makefile targets: test") || !strings.Contains(prompts[0], "git pull  (ran)") || strings.Contains(prompts[0], "<conversation>") {
		t.Errorf("%s", prompts[0])
	}
	if !strings.Contains(prompts[1], "proposed and run: ls") || strings.Contains(prompts[1], "Makefile") {
		t.Errorf("context must not leak between requests: %s", prompts[1])
	}
	if strings.Contains(prompts[3], "project:") || strings.Contains(prompts[3], "<conversation>") {
		t.Errorf("a plain request gets none: %s", prompts[3])
	}
	for cat, want := range map[string][2]int{"context": {1, 1}, "follow-up": {1, 1}, "should-ask": {0, 1}, "should-not-ask": {0, 0}} {
		if p, a := res.Tally(cat); p != want[0] || a != want[1] {
			t.Errorf("%s: %d of %d", cat, p, a)
		}
	}
}

func TestTallyIgnoresRequestsThatNeverReachedTheModel(t *testing.T) {
	res := RunBench(context.Background(), unreachable{}, nl.Env{}, []Request{{Text: "x", Category: "context"}}, 0, nil)
	if p, a := res.Tally("context"); p != 0 || a != 0 {
		t.Errorf("%d of %d", p, a)
	}
}
