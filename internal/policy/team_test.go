package policy

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func cmds(t *testing.T, line string) []shellparse.Command {
	t.Helper()
	res, err := shellparse.Parse(line)
	if err != nil {
		t.Fatal(err)
	}
	var cs []shellparse.Command
	for _, p := range res.Pipelines {
		cs = append(cs, p.Cmds...)
	}
	return cs
}

func TestParseAcceptsEveryKindOfRule(t *testing.T) {
	rules, err := Parse([]byte(`{"rules":[
		{"match":"docker system prune","block":"Use the weekly cleanup job."},
		{"match":"apt purge","snapshot":true},
		{"match":" rm ","min_level":"R3"}]}`))
	if err != nil || len(rules) != 3 {
		t.Fatalf("%v %v", rules, err)
	}
	if rules[0].Block != "Use the weekly cleanup job." || strings.Join(rules[0].Match, " ") != "docker system prune" {
		t.Errorf("%+v", rules[0])
	}
	if !rules[1].Snapshot || rules[2].MinLevel != domain.R3 || len(rules[2].Match) != 1 {
		t.Errorf("%+v %+v", rules[1], rules[2])
	}
	if rules, err := Parse([]byte(`{}`)); err != nil || rules != nil {
		t.Errorf("an empty policy asks nothing: %v %v", rules, err)
	}
}

func TestParseRejectsWhatCouldLoosenOrConfuse(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"allow field":    {`{"rules":[{"match":"rm","allow":true}]}`, "only make things stricter"},
		"max level":      {`{"rules":[{"match":"rm","max_level":"R1"}]}`, "only make things stricter"},
		"top-level key":  {`{"allow":[]}`, "only make things stricter"},
		"not json":       {`rules:`, "only make things stricter"},
		"trailing text":  {`{} {}`, "after the end"},
		"empty match":    {`{"rules":[{"match":"  ","snapshot":true}]}`, "rule 1: match is empty"},
		"empty block":    {`{"rules":[{"match":"rm","block":" "}]}`, "rule 1: block needs a reason"},
		"R0 is no raise": {`{"rules":[{"match":"rm","min_level":"R0"}]}`, "never lower"},
		"bad level":      {`{"rules":[{"match":"rm","min_level":"high"}]}`, `not "high"`},
		"does nothing":   {`{"rules":[{"match":"ls","snapshot":true},{"match":"rm"}]}`, "rule 2 does nothing"},
	} {
		_, err := Parse([]byte(tc.in))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}

func TestEvaluateMatchesTheCommandWords(t *testing.T) {
	prune := Rule{Match: []string{"docker", "system", "prune"}, Block: "no pruning"}
	purge := Rule{Match: []string{"apt", "purge"}, Snapshot: true}
	rm := Rule{Match: []string{"rm"}, MinLevel: domain.R3}
	for _, tc := range []struct {
		name, line string
		rules      []Rule
		want       Outcome
	}{
		{"exact", "docker system prune", []Rule{prune}, Outcome{Block: "no pruning"}},
		{"extra words and flags", "docker system prune -af --volumes", []Rule{prune}, Outcome{Block: "no pruning"}},
		{"flag between words", "docker --debug system prune", []Rule{prune}, Outcome{Block: "no pruning"}},
		{"sudo is unwrapped", "sudo apt -y purge nginx", []Rule{purge}, Outcome{Snapshot: true}},
		{"full path to the program", "/usr/bin/rm -r x", []Rule{rm}, Outcome{MinLevel: domain.R3}},
		{"inside a pipeline", "echo y | docker system prune", []Rule{prune}, Outcome{Block: "no pruning"}},
		{"inside sh -c", `sh -c "docker system prune"`, []Rule{prune}, Outcome{Block: "no pruning"}},
		{"shorter command", "docker system", []Rule{prune}, Outcome{}},
		{"different subcommand", "docker system df", []Rule{prune}, Outcome{}},
		{"different program", "podman system prune", []Rule{prune}, Outcome{}},
		{"no rules", "docker system prune", nil, Outcome{}},
		{"all rules combine", "rm x; apt purge y", []Rule{rm, purge}, Outcome{MinLevel: domain.R3, Snapshot: true}},
		{"highest minimum wins", "rm x", []Rule{{Match: []string{"rm"}, MinLevel: domain.R2}, rm}, Outcome{MinLevel: domain.R3}},
		{"first reason wins", "docker system prune", []Rule{prune, {Match: []string{"docker"}, Block: "second"}}, Outcome{Block: "no pruning"}},
	} {
		if got := Evaluate(tc.rules, cmds(t, tc.line)); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestApplyOnlyRaises(t *testing.T) {
	for _, tc := range []struct {
		name  string
		o     Outcome
		level domain.Level
		d     domain.Decision
		wantL domain.Level
		wantD domain.Decision
	}{
		{"nothing asked", Outcome{}, domain.R1, domain.Allow, domain.R1, domain.Allow},
		{"nothing asked keeps a stricter decision", Outcome{}, domain.R3, domain.HardBlock, domain.R3, domain.HardBlock},
		{"minimum raises level and decision", Outcome{MinLevel: domain.R3}, domain.R1, domain.Allow, domain.R3, domain.Block},
		{"minimum below the level changes nothing", Outcome{MinLevel: domain.R2}, domain.R4, domain.HardBlock, domain.R4, domain.HardBlock},
		{"block", Outcome{Block: "no"}, domain.R0, domain.Allow, domain.R0, domain.Block},
		{"block never softens a hard block", Outcome{Block: "no"}, domain.R4, domain.HardBlock, domain.R4, domain.HardBlock},
		{"snapshot makes sure you are asked", Outcome{Snapshot: true}, domain.R1, domain.Allow, domain.R1, domain.Confirm},
		{"snapshot never softens a block", Outcome{Snapshot: true}, domain.R3, domain.Block, domain.R3, domain.Block},
		{"an estimated-effect confirm survives a minimum", Outcome{MinLevel: domain.R1}, domain.R1, domain.Confirm, domain.R1, domain.Confirm},
	} {
		l, d := tc.o.Apply(tc.level, tc.d)
		if l != tc.wantL || d != tc.wantD {
			t.Errorf("%s: got %s %s, want %s %s", tc.name, l, d, tc.wantL, tc.wantD)
		}
	}
}
