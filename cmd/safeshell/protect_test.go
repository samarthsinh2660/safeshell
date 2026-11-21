package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// realLoadProtected is the real loader, kept by TestMain before it installs a fake.
var realLoadProtected func(string) ([]string, []policy.Rule, []string)

func fakeProtected(t *testing.T, folders []string, rules []policy.Rule, warnings []string) {
	t.Helper()
	old := loadProtected
	t.Cleanup(func() { loadProtected = old })
	loadProtected = func(string) ([]string, []policy.Rule, []string) { return folders, rules, warnings }
}

func TestProtectedFolderBlocksAWriteInAnalyze(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeProtected(t, []string{"/home/u/thesis"}, nil, nil)
	code, out, _ := runSS("analyze", "rm", "/home/u/thesis/a.tex")
	if code != 3 {
		t.Errorf("code %d", code)
	}
	wantAll(t, out, "R3", "Raised because this folder is on your protected list: /home/u/thesis.")
	if strings.Contains(out, "what it would really do") {
		t.Errorf("a raise by a protected folder alone is not something the command was seen to do:\n%s", out)
	}
	if code, _, _ := runSS("analyze", "rm", "/home/u/thesis2/a.tex"); code == 3 {
		t.Error("a folder with a similar name must not be blocked")
	}
	if code, _, _ := runSS("analyze", "cat", "/home/u/thesis/a.tex"); code != 0 {
		t.Errorf("reading is unchanged, code %d", code)
	}
}

func TestProtectedReasonIsShownInPlainOutputAndToAgents(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeProtected(t, []string{"/home/u/thesis"}, nil, nil)
	t.Setenv("SAFESHELL_PLAIN", "1")
	_, out, _ := runSS("analyze", "rm", "/home/u/thesis/a.tex")
	wantAll(t, out, "Raised because this folder is on your protected list: /home/u/thesis")
	a := analyze(t.Context(), "rm /home/u/thesis/a.tex", "/w")
	if ans := answerFor(a); ans.Allow || !strings.Contains(ans.Reason, "this folder is on your protected list") {
		t.Errorf("%+v", ans)
	}
}

func TestProjectWarningsAreShown(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeProtected(t, nil, nil, []string{"In /p/.safeshell.json, ignored \"allow\"."})
	_, out, errOut := runSS("analyze", "ls")
	if !strings.Contains(out+errOut, `Warning: In /p/.safeshell.json, ignored "allow".`) {
		t.Errorf("%q %q", out, errOut)
	}
}

func TestRealLoadProtectedJoinsSettingsAndProjectFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	proj := filepath.Join(home, "p")
	sub := filepath.Join(proj, "src")
	for _, d := range []string{filepath.Join(cfg, "safeshell"), sub} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cfg, "safeshell", "config.json"), `{"protect":["~/thesis","/srv/data","~"]}`)
	write(filepath.Join(proj, ".safeshell.json"), `{"protect":["deploy"],"rules":[{"match":"rm","min_level":"R3"}]}`)
	folders, rules, warns := realLoadProtected(sub)
	want := []string{home + "/thesis", "/srv/data", home, proj + "/deploy"}
	if strings.Join(folders, ",") != strings.Join(want, ",") || len(rules) != 1 || len(warns) != 0 {
		t.Errorf("%v %v %v", folders, rules, warns)
	}

	write(filepath.Join(cfg, "safeshell", "config.json"), `{"protect":["thesis"]}`)
	folders, _, warns = realLoadProtected(sub)
	if len(warns) != 1 || !strings.Contains(warns[0], "settings file") || len(folders) != 1 {
		t.Errorf("a broken settings file is reported and the project file still applies: %v %v", folders, warns)
	}
}
