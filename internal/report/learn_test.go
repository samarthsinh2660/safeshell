package report

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func TestLearn(t *testing.T) {
	cp := domain.Finding{Level: domain.R1, Command: "cp a b", Reason: "Copies files."}
	for name, tc := range map[string]struct {
		v    domain.Verdict
		want string
	}{
		"read-only teaches nothing": {domain.Verdict{Level: domain.R0, Findings: []domain.Finding{{Level: domain.R0, Reason: "Only reads."}}}, ""},
		"reason without a safer form": {domain.Verdict{Level: domain.R1, Findings: []domain.Finding{cp}},
			"Learn: R1 Reversible. Copies files."},
		"reason and safer form": {domain.Verdict{Level: domain.R1, Findings: []domain.Finding{cp.WithAlt("cp -n a b", "does not overwrite")}},
			"Learn: R1 Reversible. Copies files. Safer: cp -n a b (does not overwrite)"},
		"the finding that set the level is used": {domain.Verdict{Level: domain.R1, Findings: []domain.Finding{
			{Level: domain.R0, Reason: "Only reads."}, cp, {Level: domain.R1, Reason: "Second."}}},
			"Learn: R1 Reversible. Copies files."},
		"no finding": {domain.Verdict{Level: domain.R1}, "Learn: R1 Reversible."},
		"untrusted text is made printable": {domain.Verdict{Level: domain.R1, Findings: []domain.Finding{
			cp.WithAlt("cp -n a \x1b[31mb", "ok")}}, `Safer: cp -n a \x1b[31mb (ok)`},
	} {
		got := Learn(tc.v)
		if !strings.Contains(got, tc.want) || (tc.want == "" && got != "") || strings.ContainsRune(got, 0x1b) {
			t.Errorf("%s: %q", name, got)
		}
	}
	if got := Learn(domain.Verdict{Level: domain.R1, Findings: []domain.Finding{cp, {Level: domain.R1, Reason: "Second."}}}); strings.Contains(got, "Second") {
		t.Errorf("one line only: %q", got)
	}
}
