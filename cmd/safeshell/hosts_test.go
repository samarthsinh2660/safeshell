package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realLoadHosts is the real loader, kept by TestMain before it installs a fake.
var realLoadHosts func() (map[string]string, error)

func fakeHosts(t *testing.T, hosts map[string]string, err error) {
	t.Helper()
	old := loadHosts
	t.Cleanup(func() { loadHosts = old })
	loadHosts = func() (map[string]string, error) { return hosts, err }
}

func TestProdHostRaisesARemoteWriteInAnalyze(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeHosts(t, map[string]string{"prod*": "prod"}, nil)
	code, out, _ := runSS("analyze", "ssh", "prod1", "'rm -rf /var/tmp/x'")
	if code != 4 {
		t.Errorf("code %d", code)
	}
	wantAll(t, out, "R4", "Raised because this is a production host (prod1).")
	code, out, _ = runSS("analyze", "ssh", "dev1", "'rm -rf /var/tmp/x'")
	if code != 3 || strings.Contains(out, "production host") {
		t.Errorf("an unlabelled host is unchanged: code %d\n%s", code, out)
	}
}

func TestInteractiveSshToALabelledHostSaysItIsNotChecked(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeHosts(t, map[string]string{"prod*": "prod"}, nil)
	code, out, errOut := runSS("guard", "--", "ssh prod1")
	if code != 0 || out != "ssh prod1\n" {
		t.Errorf("it must not block: code %d out %q", code, out)
	}
	wantAll(t, errOut, "prod1 is a labelled host. Commands you type there are only checked if SafeShell is installed on it.")
	if _, _, errOut := runSS("guard", "--", "ssh dev1"); strings.Contains(errOut, "labelled host") {
		t.Errorf("an unlabelled host gets no note:\n%s", errOut)
	}
}

func TestUnusableSettingsGiveAWarningAndNoLabels(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeHosts(t, map[string]string{"prod*": "prod"}, errors.New("bad hosts"))
	_, _, errOut := runSS("guard", "--", "ls")
	wantAll(t, errOut, "Warning: host labels were ignored", "bad hosts")
	if h, w := hostLabels(); h != nil || !strings.Contains(w, "bad hosts") {
		t.Errorf("%v %q", h, w)
	}
}

func TestHostLabelsComeFromTheSettingsFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if h, err := realLoadHosts(); err != nil || h != nil {
		t.Errorf("no file, no labels: %v %v", h, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "safeshell"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "safeshell", "config.json"), []byte(`{"hosts":{"prod*":"prod"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if h, err := realLoadHosts(); err != nil || h["prod*"] != "prod" {
		t.Errorf("%v %v", h, err)
	}
}
