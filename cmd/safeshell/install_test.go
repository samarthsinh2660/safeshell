package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/platform"
)

func TestInstallAndUninstallNeedAKnownShell(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "")
	if code, _, e := runSS("install"); code != 1 || !strings.Contains(e, "only bash and zsh") || !strings.Contains(e, "safeshell hook") {
		t.Errorf("install: %d %q", code, e)
	}
	if code, _, e := runSS("uninstall"); code != 1 || !strings.Contains(e, "only bash and zsh") {
		t.Errorf("uninstall: %d %q", code, e)
	}
}

func TestInstallReportsAStartupFileItCannotChange(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	if err := os.Mkdir(filepath.Join(home, ".bashrc"), 0o755); err != nil { // a folder, not a file
		t.Fatal(err)
	}
	if code, _, e := runSS("install"); code != 1 || !strings.Contains(e, "safeshell install:") {
		t.Errorf("install: %d %q", code, e)
	}
	if code, _, e := runSS("uninstall"); code != 1 || !strings.Contains(e, "safeshell uninstall:") {
		t.Errorf("uninstall: %d %q", code, e)
	}
}

func TestInstallExplainsAMissingSandboxAndChainsAISetup(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("SAFESHELL_AI", "sometimes") // ai setup fails at once, so nothing is downloaded
	old := probe
	t.Cleanup(func() { probe = old })
	probe = func() platform.Report { return platform.Report{AppArmorRestrictsUserNS: true, UserNSErr: "denied"} }
	code, out, e := runSS("install", "--ai")
	if code != 1 || !strings.Contains(out, "The sandbox is not available") || !strings.Contains(out, "install-apparmor.sh") || !strings.Contains(e, "SAFESHELL_AI") {
		t.Errorf("%d %q %q", code, out, e)
	}
}

func TestUninstallSaysWhenTheEnvironmentStillHasTheHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("SAFESHELL_OFF", "")
	if code, _, _ := runSS("install"); code != 0 {
		t.Fatal("install failed")
	}
	if _, out, _ := runSS("uninstall"); !strings.Contains(out, "This terminal still has it") {
		t.Errorf("%q", out)
	}
}

func TestUseColorNeedsATerminalAndNoNOCOLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	f, err := os.Open(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("expected a missing file")
	}
	if useColor(f) { // a nil-ish file cannot be a terminal
		t.Error("a missing file is not a terminal")
	}
	plain, _ := os.Create(filepath.Join(t.TempDir(), "plain"))
	defer plain.Close()
	if useColor(plain) {
		t.Error("a plain file is not a terminal")
	}
	null, _ := os.OpenFile(os.DevNull, os.O_WRONLY, 0) // a character device, like a terminal
	defer null.Close()
	if !useColor(null) {
		t.Error("a character device counts as a terminal")
	}
	t.Setenv("NO_COLOR", "1")
	if useColor(null) {
		t.Error("NO_COLOR turns colour off")
	}
}
