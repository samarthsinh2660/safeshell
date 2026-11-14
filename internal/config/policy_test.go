package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// policyFile makes a folder holding policy.json with the given modes and content.
func policyFile(t *testing.T, dirMode, fileMode os.FileMode, content string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), "safeshell")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(d, "policy.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, mode := range map[string]os.FileMode{p: fileMode, d: dirMode} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.Chmod(d, 0o755); os.Chmod(p, 0o644) })
	return p
}

const goodPolicy = `{"rules":[{"match":"docker system prune","block":"no"},{"match":"apt purge","snapshot":true}]}`

func TestLoadPolicyReadsATrustedFile(t *testing.T) {
	rules, found, err := LoadPolicy(policyFile(t, 0o755, 0o644, goodPolicy), uint32(os.Getuid()))
	if err != nil || !found || len(rules) != 2 {
		t.Fatalf("%v %v %v", rules, found, err)
	}
}

func TestLoadPolicyMissingIsNotAnError(t *testing.T) {
	rules, found, err := LoadPolicy(filepath.Join(t.TempDir(), "policy.json"), 0)
	if err != nil || found || rules != nil {
		t.Errorf("%v %v %v", rules, found, err)
	}
}

func TestLoadPolicyRefusesAnUntrustedFile(t *testing.T) {
	me := uint32(os.Getuid())
	for name, tc := range map[string]struct {
		dirMode, fileMode os.FileMode
		owner             uint32
	}{
		"group-writable file":  {0o755, 0o664, me},
		"world-writable file":  {0o755, 0o666, me},
		"group-writable dir":   {0o775, 0o644, me},
		"world-writable dir":   {0o777, 0o644, me},
		"someone else's files": {0o755, 0o644, me + 1},
	} {
		_, found, err := LoadPolicy(policyFile(t, tc.dirMode, tc.fileMode, goodPolicy), tc.owner)
		if err == nil || !found || !strings.Contains(err.Error(), "ignored") || !strings.Contains(err.Error(), "sudo chown root:root") {
			t.Errorf("%s: found %v err %v", name, found, err)
		}
	}
}

func TestLoadPolicyReportsBadContent(t *testing.T) {
	_, found, err := LoadPolicy(policyFile(t, 0o755, 0o644, `{"rules":[{"match":"rm","allow":true}]}`), uint32(os.Getuid()))
	if err == nil || !found || !strings.Contains(err.Error(), "ignored") || !strings.Contains(err.Error(), "only make things stricter") {
		t.Errorf("found %v err %v", found, err)
	}
}

func TestLoadPolicyReportsUnreadableAndOversizedFiles(t *testing.T) {
	me := uint32(os.Getuid())
	if os.Getuid() != 0 {
		_, found, err := LoadPolicy(policyFile(t, 0o755, 0, goodPolicy), me)
		if err == nil || !found || !strings.Contains(err.Error(), "cannot read") {
			t.Errorf("unreadable: found %v err %v", found, err)
		}
	}
	if _, _, err := LoadPolicy(filepath.Join(policyFile(t, 0o755, 0o644, goodPolicy), "x"), me); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("a path under a file: %v", err)
	}
	sub := filepath.Join(filepath.Dir(policyFile(t, 0o755, 0o644, goodPolicy)), "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadPolicy(sub, me); err == nil || !strings.Contains(err.Error(), "cannot read") {
		t.Errorf("a folder is not a policy: %v", err)
	}
	big := strings.Repeat(" ", maxPolicyBytes+1)
	if _, _, err := LoadPolicy(policyFile(t, 0o755, 0o644, big), me); err == nil || !strings.Contains(err.Error(), "too big") {
		t.Errorf("oversized: %v", err)
	}
}

func TestTrustedNeedsStatInformation(t *testing.T) {
	if trusted(nil, 0) {
		t.Error("nothing known is not trusted")
	}
}
