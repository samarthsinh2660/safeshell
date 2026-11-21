package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPolicyJournal(t *testing.T) {
	me := uint32(os.Getuid())
	for _, tc := range []struct {
		name  string
		path  string
		owner uint32
		want  bool
	}{
		{"asked for", policyFile(t, 0o755, 0o644, `{"journal":true}`), me, true},
		{"not asked for", policyFile(t, 0o755, 0o644, goodPolicy), me, false},
		{"no file", filepath.Join(t.TempDir(), "policy.json"), me, false},
		{"not trusted", policyFile(t, 0o755, 0o644, `{"journal":true}`), me + 1, false},
		{"damaged", policyFile(t, 0o755, 0o644, `{"journal":`), me, false},
	} {
		if got := LoadPolicyJournal(tc.path, tc.owner); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
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
