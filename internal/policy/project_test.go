package policy

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func TestProjectFileCanTighten(t *testing.T) {
	p, warns := ParseProject([]byte(`{
		"protect": ["deploy", "./infra/prod/", "a/../docs"],
		"rules": [
			{"match":"rm -rf","min_level":"R4"},
			{"match":"terraform apply","block":" Use the pipeline. "},
			{"match":"make clean","snapshot":true}]}`), "/p")
	if len(warns) != 0 {
		t.Fatalf("%v", warns)
	}
	if strings.Join(p.Protect, ",") != "/p/deploy,/p/infra/prod,/p/docs" || len(p.Rules) != 3 {
		t.Errorf("%+v", p)
	}
	if p.Rules[0].MinLevel != domain.R4 || !p.Rules[2].Snapshot || p.Rules[1].Block != "this project's .safeshell.json says: Use the pipeline." {
		t.Errorf("%+v", p.Rules)
	}
}

func TestProjectFileIgnoresWhatWouldLoosen(t *testing.T) {
	p, warns := ParseProject([]byte(`{
		"allow": ["rm -rf"],
		"protect": ["../secrets", "/etc", "", "ok", ".."],
		"rules": [
			{"match":"rm -rf","min_level":"R0"},
			{"match":"rm","allow":true},
			{"match":"rm"},
			{"match":"ls","min_level":"R2"}]}`), "/p")
	if strings.Join(p.Protect, ",") != "/p/ok" || len(p.Rules) != 1 || p.Rules[0].MinLevel != domain.R2 {
		t.Errorf("only the tightening parts are kept: %+v", p)
	}
	got := strings.Join(warns, "\n")
	for _, want := range []string{`ignored "allow"`, `"protect" entry "../secrets"`, `"protect" entry "/etc"`, `"protect" entry ""`, `"protect" entry ".."`,
		`"rules" entry 1`, `never lower one`, `"rules" entry 2`, `"rules" entry 3`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if len(warns) != 8 {
		t.Errorf("%d warnings:\n%s", len(warns), got)
	}
}

func TestProjectFileWithTheWrongShapesIsReported(t *testing.T) {
	for name, in := range map[string]string{
		"not json":         `{"protect":`,
		"not an object":    `[]`,
		"protect a string": `{"protect":"deploy"}`,
		"rules an object":  `{"rules":{}}`,
	} {
		p, warns := ParseProject([]byte(in), "/p")
		if len(warns) != 1 || len(p.Protect) != 0 || len(p.Rules) != 0 {
			t.Errorf("%s: %+v %v", name, p, warns)
		}
	}
	if p, warns := ParseProject([]byte(`{}`), "/p"); len(warns) != 0 || len(p.Rules) != 0 {
		t.Errorf("%+v %v", p, warns)
	}
}
