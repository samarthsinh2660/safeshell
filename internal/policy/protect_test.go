package policy

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func TestUnderStopsAtAFolderBoundary(t *testing.T) {
	for _, tc := range []struct {
		dir, p string
		want   bool
	}{
		{"/h/thesis", "/h/thesis", true},
		{"/h/thesis", "/h/thesis/a.tex", true},
		{"/h/thesis/", "/h/thesis/sub/../a.tex", true},
		{"/h/thesis", "/h/thesis2/a.tex", false},
		{"/h/thesis", "/h/thesis/../other", false},
		{"/h/thesis", "/h", false},
		{"/", "/anything", true},
	} {
		if got := Under(tc.dir, tc.p); got != tc.want {
			t.Errorf("Under(%q, %q) = %v", tc.dir, tc.p, got)
		}
	}
}

func TestEvaluateProtected(t *testing.T) {
	folders := []string{"/srv/data", "/h/thesis"}
	for name, tc := range map[string]struct {
		changed []string
		hit     bool
	}{
		"a file inside":      {[]string{"/h/thesis/a.tex"}, true},
		"the folder itself":  {[]string{"/h/thesis"}, true},
		"a folder above it":  {[]string{"/h"}, true},
		"a similar name":     {[]string{"/h/thesis2/a.tex"}, false},
		"somewhere else":     {[]string{"/tmp/x", "/h/other"}, false},
		"nothing is changed": {nil, false},
	} {
		o := EvaluateProtected(folders, tc.changed)
		if tc.hit != (o.MinLevel == domain.R3) || tc.hit != (o.Reason != "") {
			t.Errorf("%s: %+v", name, o)
		}
	}
	if o := EvaluateProtected(folders, []string{"/srv/data/x"}); o.Reason != "this folder is on your protected list: /srv/data" {
		t.Errorf("the reason names the folder: %q", o.Reason)
	}
}

func TestProtectedApplyOnlyRaises(t *testing.T) {
	o := ProtectOutcome{MinLevel: domain.R3}
	if l, d := o.Apply(domain.R1, domain.Allow); l != domain.R3 || d != domain.Block {
		t.Errorf("%s %s", l, d)
	}
	if l, d := o.Apply(domain.R4, domain.HardBlock); l != domain.R4 || d != domain.HardBlock {
		t.Errorf("must not lower: %s %s", l, d)
	}
	if l, d := (ProtectOutcome{}).Apply(domain.R2, domain.Confirm); l != domain.R2 || d != domain.Confirm {
		t.Errorf("%s %s", l, d)
	}
}
