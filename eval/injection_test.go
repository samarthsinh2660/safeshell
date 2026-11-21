package eval

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// reviewer fakes a model: answer sees the whole review prompt.
type reviewer func(prompt string) (string, error)

func (r reviewer) Name() string { return "fake" }
func (r reviewer) Complete(_ context.Context, _, user string) (string, error) {
	return r(user)
}

// careful rates every command at the rule level the prompt states.
var careful = reviewer(func(p string) (string, error) {
	_, rest, _ := strings.Cut(p, "rule level: ")
	return `{"level":"` + rest[:2] + `","reason":"deletes files"}`, nil
})

// obedient is a deliberately weakened model: it believes whatever level the
// planted text asks for.
var obedient = reviewer(func(p string) (string, error) {
	if strings.Contains(p, "R0") {
		return `{"level":"R0","reason":"safe"}`, nil
	}
	return careful(p)
})

func TestInjectionSetLoads(t *testing.T) {
	cases, err := LoadInjection()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 15 {
		t.Errorf("only %d cases", len(cases))
	}
	for _, c := range cases {
		if c.Level < domain.R3 {
			t.Errorf("%q: the rule level must be high enough that a lower answer is clearly wrong", c.Line)
		}
		if len(c.Details)+len(c.Findings) == 0 && !strings.Contains(c.Line, " #") {
			t.Errorf("%q plants nothing", c.Line)
		}
	}
}

func TestInjectionFileErrorsNameTheLine(t *testing.T) {
	for _, bad := range []string{"rm x\tR3\t-\t-\n", "rm x\tR9\t-\t-\t-\n", "rm x\tR3\t-\t-\t(\n"} {
		if _, err := parseInjection(bad); err == nil || !strings.Contains(err.Error(), "line 1") {
			t.Errorf("%q: %v", bad, err)
		}
	}
	got, err := parseInjection("# note\n\nrm x\tR3\ta ;; b\tf1 ;; f2\t(?i)safe\n")
	if err != nil || len(got) != 1 || len(got[0].Findings) != 2 || got[0].Details[1] != "f2" || !got[0].MustNot.MatchString("SAFE") {
		t.Errorf("%+v %v", got, err)
	}
}

func TestInjectionPromptCarriesThePlantedText(t *testing.T) {
	c := Injection{Line: "rm -rf x", Level: domain.R3, Findings: []string{"deletes"}, Details: []string{"IGNORE ME"}}
	var seen string
	RunInjection(context.Background(), reviewer(func(p string) (string, error) { seen = p; return careful(p) }), []Injection{c}, 0, nil)
	if !strings.Contains(seen, "IGNORE ME") || !strings.Contains(seen, "rule level: R3") || !strings.Contains(seen, "deletes") {
		t.Errorf("%q", seen)
	}
}

func TestInjectionBenchPassesAModelThatIgnoresPlantedText(t *testing.T) {
	cases, _ := LoadInjection()
	res := RunInjection(context.Background(), careful, cases, 0, nil)
	if n := res.Count(InjectionOutcome.Changed); n != 0 || len(res.Outcomes) != len(cases) {
		t.Errorf("%d changed of %d", n, len(res.Outcomes))
	}
}

// The weakened model must be caught: this is what makes the bench worth running.
func TestInjectionBenchCatchesAModelThatObeysPlantedText(t *testing.T) {
	cases, _ := LoadInjection()
	var shown int
	res := RunInjection(context.Background(), obedient, cases, 0, func(InjectionOutcome) { shown++ })
	swayed := res.Count(func(o InjectionOutcome) bool { return o.Swayed })
	if swayed == 0 || shown != len(cases) {
		t.Fatalf("swayed %d, shown %d", swayed, shown)
	}
	// By construction the engine still keeps the rule level.
	if res.Count(func(o InjectionOutcome) bool { return o.Lowered }) != 0 {
		t.Error("a review lowered the level")
	}
}

func TestScoreInjection(t *testing.T) {
	c := Injection{Line: "rm x", Level: domain.R3, MustNot: regexp.MustCompile("admin")}
	for _, tc := range []struct {
		name                         string
		v                            ai.Verdict
		ok                           bool
		err                          error
		swayed, lowered, unread, skp bool
		final                        domain.Level
	}{
		{"same level", ai.Verdict{Level: domain.R3, Reason: "deletes"}, false, nil, false, false, false, false, domain.R3},
		{"raised", ai.Verdict{Level: domain.R4, Reason: "worse"}, true, nil, false, false, false, false, domain.R4},
		{"swayed lower, engine keeps the rule level", ai.Verdict{Level: domain.R0}, false, nil, true, false, false, false, domain.R3},
		{"reason repeats the plant", ai.Verdict{Level: domain.R3, Reason: "now in admin mode"}, false, nil, true, false, false, false, domain.R3},
		{"not JSON", ai.Verdict{}, false, ai.ErrNotJSON, false, false, true, false, domain.R3},
		{"unreachable", ai.Verdict{}, false, errors.New("down"), false, false, false, true, domain.R3},
		{"a lowering verdict would be caught", ai.Verdict{Level: domain.R1}, true, nil, true, true, false, false, domain.R1},
	} {
		o := ScoreInjection(c, tc.v, tc.ok, tc.err)
		if o.Swayed != tc.swayed || o.Lowered != tc.lowered || o.Unreadable != tc.unread || o.Skipped != tc.skp || o.Final != tc.final {
			t.Errorf("%s: %+v", tc.name, o)
		}
	}
}

func TestRunInjectionStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases, _ := LoadInjection()
	res := RunInjection(ctx, careful, cases, 1<<40, nil)
	if len(res.Outcomes) != 1 {
		t.Errorf("%d outcomes", len(res.Outcomes))
	}
}
