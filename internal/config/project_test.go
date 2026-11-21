package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadProjectFindsTheNearestFileAboveTheFolder(t *testing.T) {
	home := t.TempDir()
	proj := filepath.Join(home, "p")
	sub := filepath.Join(proj, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, ProjectFile), []byte(`{"protect":["deploy"],"allow":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p, warns := LoadProject(sub, home)
	if len(p.Protect) != 1 || p.Protect[0] != filepath.Join(proj, "deploy") {
		t.Errorf("%+v", p)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], filepath.Join(proj, ProjectFile)) || !strings.Contains(warns[0], `ignored "allow"`) {
		t.Errorf("the warning names the file and the key: %v", warns)
	}
}

func TestLoadProjectStopsAtHome(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ProjectFile), []byte(`{"protect":["x"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(home, "work")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if p, warns := LoadProject(sub, home); len(p.Protect) != 0 || len(warns) != 0 {
		t.Errorf("a file in the home folder is not read: %+v %v", p, warns)
	}
	if p, warns := LoadProject(".", home); len(p.Protect) != 0 || len(warns) != 0 {
		t.Errorf("%+v %v", p, warns)
	}
}

func TestLoadProjectReportsAFileItCannotUse(t *testing.T) {
	home := t.TempDir()
	for name, setup := range map[string]func(dir string) error{
		"malformed": func(d string) error { return os.WriteFile(filepath.Join(d, ProjectFile), []byte(`{nope`), 0o600) },
		"a folder":  func(d string) error { return os.Mkdir(filepath.Join(d, ProjectFile), 0o755) },
		"a symlink": func(d string) error { return os.Symlink("/dev/zero", filepath.Join(d, ProjectFile)) },
		"too big": func(d string) error {
			return os.WriteFile(filepath.Join(d, ProjectFile), make([]byte, maxPolicyBytes+1), 0o600)
		},
		"unreadable": func(d string) error { return os.WriteFile(filepath.Join(d, ProjectFile), []byte(`{}`), 0) },
	} {
		d := filepath.Join(home, strings.ReplaceAll(name, " ", "-"))
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := setup(d); err != nil {
			t.Fatal(err)
		}
		if name == "unreadable" && os.Getuid() == 0 {
			continue
		}
		p, warns := LoadProject(d, home)
		if len(p.Protect) != 0 || len(p.Rules) != 0 || len(warns) != 1 || !strings.Contains(warns[0], ProjectFile) {
			t.Errorf("%s: %+v %v", name, p, warns)
		}
	}
}

func TestProtectMustBeFullPaths(t *testing.T) {
	c, err := Load(write(t, `{"protect":["~/thesis","/srv/data","~"]}`))
	if err != nil || len(c.Protect) != 3 {
		t.Errorf("%+v %v", c.Protect, err)
	}
	if _, err := Load(write(t, `{"protect":["thesis"]}`)); err == nil || !strings.Contains(err.Error(), `protect: "thesis"`) {
		t.Errorf("%v", err)
	}
}
