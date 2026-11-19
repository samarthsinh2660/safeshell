package policy

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

var testHosts = map[string]string{"prod*": Prod, "db-*.example.com": Prod, "staging-*": Staging}

func TestHostLabel(t *testing.T) {
	for _, c := range []struct{ host, want string }{
		{"prod1", Prod},
		{"PROD1", Prod},
		{"deploy@prod1", Prod},
		{"db-3.example.com", Prod},
		{"db-3.example.org", ""},
		{"staging-web", Staging},
		{"dev1", ""},
		{"", ""},
	} {
		if got := HostLabel(testHosts, c.host); got != c.want {
			t.Errorf("%q: got %q, want %q", c.host, got, c.want)
		}
	}
	if got := HostLabel(map[string]string{"app-*": Staging, "*-1": Prod}, "app-1"); got != Prod {
		t.Errorf("prod must win when both match: %q", got)
	}
	if got := HostLabel(map[string]string{"*-1": Prod, "app-*": Staging}, "app-1"); got != Prod {
		t.Errorf("prod must win whatever the order: %q", got)
	}
}

func TestValidateHosts(t *testing.T) {
	if err := ValidateHosts(testHosts); err != nil {
		t.Fatal(err)
	}
	if err := ValidateHosts(nil); err != nil {
		t.Fatal(err)
	}
	for name, h := range map[string]map[string]string{
		"bad label":     {"prod*": "production"},
		"empty label":   {"prod*": ""},
		"bad pattern":   {"prod[": Prod},
		"empty pattern": {"": Prod},
	} {
		if err := ValidateHosts(h); err == nil || !strings.HasPrefix(err.Error(), "hosts:") {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestEvaluateHosts(t *testing.T) {
	for _, c := range []struct {
		line   string
		level  domain.Level // the raised minimum
		reason string
		note   bool
	}{
		{"ssh prod1 'rm -rf /var/tmp/x'", domain.R4, "production host (prod1)", false},
		{"ssh deploy@prod1 'rm -rf /var/tmp/x'", domain.R4, "production host (deploy@prod1)", false},
		{"ssh -p 2222 prod1 'rm -rf /var/tmp/x'", domain.R4, "production host (prod1)", false},
		{"ssh -l deploy -i key prod1 'rm -rf /var/tmp/x'", domain.R4, "production host (prod1)", false},
		{"ssh -t db-3.example.com 'rm -rf /var/tmp/x'", domain.R4, "production host (db-3.example.com)", false},
		{"ssh prod1 'rm /var/tmp/x'", domain.R3, "production host (prod1)", false}, // R2 -> R3
		{"ssh prod1 'rm -rf /'", domain.R4, "production host (prod1)", false},      // stays at the top
		{"ssh prod1 'ls /var'", 0, "", false},
		{"ssh prod1 'mv /a /b'", 0, "", false},
		{"ssh staging-web 'rm -rf /var/tmp/x'", 0, "", false},
		{"ssh dev1 'rm -rf /var/tmp/x'", 0, "", false},
		{"rm -rf /var/tmp/x", 0, "", false},
		{"ssh prod1", 0, "", true},
		{"ssh -p 2222 deploy@prod1", 0, "", true},
		{"ssh staging-web", 0, "", true},
		{"ssh dev1", 0, "", false},
		{"ssh", 0, "", false},
		{"ls && ssh prod1 'ls'", 0, "", false},
	} {
		v := classify.Classify(c.line, "/home/u")
		o := EvaluateHosts(testHosts, v, cmds(t, c.line))
		if o.MinLevel != c.level || (c.reason == "") != (o.Reason == "") || !strings.Contains(o.Reason, c.reason) || (o.Note != "") != c.note {
			t.Errorf("%s: %+v", c.line, o)
		}
		if c.note && !strings.Contains(o.Note, "only checked if SafeShell is installed") {
			t.Errorf("%s: note %q", c.line, o.Note)
		}
	}
}

func TestHostsWithoutLabelsAskNothing(t *testing.T) {
	line := "ssh prod1 'rm -rf /var/tmp/x'"
	if o := EvaluateHosts(nil, classify.Classify(line, "/home/u"), cmds(t, line)); o != (HostOutcome{}) {
		t.Errorf("%+v", o)
	}
}

func TestHostApplyOnlyRaises(t *testing.T) {
	o := HostOutcome{MinLevel: domain.R4}
	if l, d := o.Apply(domain.R3, domain.Block); l != domain.R4 || d != domain.HardBlock {
		t.Errorf("%s %s", l, d)
	}
	if l, d := (HostOutcome{}).Apply(domain.R3, domain.Block); l != domain.R3 || d != domain.Block {
		t.Errorf("a quiet outcome changes nothing: %s %s", l, d)
	}
	if l, d := (HostOutcome{MinLevel: domain.R2}).Apply(domain.R4, domain.HardBlock); l != domain.R4 || d != domain.HardBlock {
		t.Errorf("never lowers: %s %s", l, d)
	}
}
