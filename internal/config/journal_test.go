package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

func TestLoadTeamReportsJournalAndKeepsRules(t *testing.T) {
	me := uint32(os.Getuid())
	f, found, err := LoadTeam(policyFile(t, 0o755, 0o644, `{"journal":true,"strict":true,"rules":[{"match":"rm","min_level":"R3"}]}`), me)
	if err != nil || !found || !f.Journal || !f.Strict || len(f.Rules) != 1 {
		t.Errorf("%+v %v %v", f, found, err)
	}
	if f, _, _ := LoadTeam(policyFile(t, 0o755, 0o644, goodPolicy), me); f.Journal {
		t.Error("journal is off unless the policy asks")
	}
	if f, found, _ := LoadTeam(policyFile(t, 0o755, 0o644, `{"journal":true}`), me+1); found && f.Journal {
		t.Error("a policy that is not trusted must not turn anything on")
	}
	if _, err := policy.ParseFile([]byte(`{"journal":"yes"}`)); err == nil {
		t.Error("a journal setting that is not true or false must be refused")
	}
}

func TestAuditJournalSettingIsLoaded(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(`{"audit":{"journal":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(p); err != nil || !c.Audit.Journal {
		t.Errorf("%+v %v", c.Audit, err)
	}
	if c, _ := Load(filepath.Join(t.TempDir(), "none.json")); c.Audit.Journal {
		t.Error("the journal is off by default")
	}
}
