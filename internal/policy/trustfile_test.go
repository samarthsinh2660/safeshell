package policy

import (
	"strings"
	"testing"
)

func TestTeamPolicyCanTurnTrustOffAndHasNoWayToTurnItOn(t *testing.T) {
	f, err := ParseFile([]byte(`{"no_trust":true}`))
	if err != nil || !f.NoTrust || len(f.Rules) != 0 {
		t.Errorf("%+v %v", f, err)
	}
	if f, err := ParseFile([]byte(`{"rules":[{"match":"rm","snapshot":true}]}`)); err != nil || f.NoTrust {
		t.Errorf("a policy without no_trust leaves trust as the user set it: %+v %v", f, err)
	}
	for _, s := range []string{`{"trust":true}`, `{"trusted":[{"line":"rm x","dir":"/a"}]}`} {
		if _, err := ParseFile([]byte(s)); err == nil || !strings.Contains(err.Error(), "no_trust") {
			t.Errorf("%s: a policy cannot add trust: %v", s, err)
		}
	}
}

func TestProjectFileCannotAddTrustOrSwitchItOff(t *testing.T) {
	p, warns := ParseProject([]byte(`{
		"trusted": [{"line":"rm build/x.o","dir":"/p"}],
		"no_trust": false,
		"trust": true,
		"rules": [{"match":"rm","min_level":"R2","trusted":true}]}`), "/p")
	if len(p.Protect) != 0 || len(p.Rules) != 0 {
		t.Errorf("nothing from a project file may loosen: %+v", p)
	}
	got := strings.Join(warns, "\n")
	for _, want := range []string{`ignored "trusted"`, `ignored "no_trust"`, `ignored "trust"`, `"rules" entry 1`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
