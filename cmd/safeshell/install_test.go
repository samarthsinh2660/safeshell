package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/platform"
)

func TestInstallAndUninstallNeedAKnownShell(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "")
	if code, _, e := runSS("install"); code != 1 || !strings.Contains(e, "only bash, zsh and fish") || !strings.Contains(e, "safeshell hook") {
		t.Errorf("install: %d %q", code, e)
	}
	if code, _, e := runSS("uninstall"); code != 1 || !strings.Contains(e, "only bash, zsh and fish") {
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

func fakeExecutable(t *testing.T, path string, err error) {
	t.Helper()
	old := executablePath
	t.Cleanup(func() { executablePath = old })
	executablePath = func() (string, error) { return path, err }
}

func TestInstallUpdatesABlockThatPointsAtAnotherBinary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	fakeExecutable(t, "/old/bin/safeshell", nil)
	runSS("install")
	fakeExecutable(t, "/usr/bin/safeshell", nil)
	code, out, _ := runSS("install")
	b, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if code != 0 || !strings.Contains(out, "Updated the SafeShell hook in "+filepath.Join(home, ".bashrc")) ||
		!strings.Contains(string(b), "/usr/bin/safeshell") || strings.Contains(string(b), "/old/bin") {
		t.Errorf("%d %q\n%s", code, out, b)
	}
}

func TestInstallAndHookNeedToKnowWhereSafeshellIs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/bash")
	fakeExecutable(t, "", errors.New("gone"))
	if code, _, e := runSS("install"); code != 1 || !strings.Contains(e, "gone") {
		t.Errorf("install: %d %q", code, e)
	}
	if code, _, e := runSS("hook", "bash"); code != 1 || !strings.Contains(e, "gone") {
		t.Errorf("hook: %d %q", code, e)
	}
}

func TestHookScriptNamesTheBinaryItCalls(t *testing.T) {
	fakeExecutable(t, "/opt/safe shell/safeshell", nil)
	for _, sh := range []string{"bash", "zsh"} {
		if code, out, _ := runSS("hook", sh); code != 0 || !strings.Contains(out, "__ss_bin='/opt/safe shell/safeshell'") {
			t.Errorf("%s: %d %q", sh, code, out)
		}
	}
}

func TestInstallAndUninstallForFish(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/usr/bin/fish")
	fakeExecutable(t, "/opt/ss/safeshell", nil)
	file := filepath.Join(home, ".config", "fish", "conf.d", "safeshell.fish")
	if code, out, _ := runSS("install"); code != 0 || !strings.Contains(out, "Added the SafeShell hook to "+file) {
		t.Fatalf("%d %q", code, out)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "test -x '/opt/ss/safeshell'; and '/opt/ss/safeshell' hook fish | source") {
		t.Errorf("%s", b)
	}
	if code, out, _ := runSS("uninstall"); code != 0 || !strings.Contains(out, "Removed the SafeShell hook from "+file) {
		t.Fatalf("%d %q", code, out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("uninstall must delete the file")
	}
}

// fakeSystem points --system at a temp root and sets whether we are root.
func fakeSystem(t *testing.T, root bool) string {
	t.Helper()
	oldRoot, oldIs := systemRoot, isRoot
	t.Cleanup(func() { systemRoot, isRoot = oldRoot, oldIs })
	dir := t.TempDir()
	systemRoot, isRoot = dir, func() bool { return root }
	return dir
}

func TestSystemInstallAndUninstallNeedRoot(t *testing.T) {
	dir := fakeSystem(t, false)
	for _, cmd := range []string{"install", "uninstall"} {
		code, _, e := runSS(cmd, "--system")
		if code != 1 || !strings.Contains(e, "must run as root") || !strings.Contains(e, "sudo safeshell "+cmd+" --system") {
			t.Errorf("%s: %d %q", cmd, code, e)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "etc")); !os.IsNotExist(err) {
		t.Error("nothing may be written without root")
	}
}

func TestSystemInstallAndUninstallRoundTrip(t *testing.T) {
	dir := fakeSystem(t, true)
	fakeExecutable(t, "/usr/bin/safeshell", nil)
	file := filepath.Join(dir, "etc/profile.d/safeshell.sh")
	if code, out, e := runSS("install", "--system"); code != 0 || !strings.Contains(out, "Added the SafeShell hook for every user") || !strings.Contains(out, file) {
		t.Fatalf("%d %q %q", code, out, e)
	}
	if code, out, _ := runSS("install", "--system"); code != 0 || !strings.Contains(out, "already set up") {
		t.Errorf("second run: %d %q", code, out)
	}
	if code, out, _ := runSS("uninstall", "--system"); code != 0 || !strings.Contains(out, "Removed the SafeShell hook for every user") || !strings.Contains(out, file) {
		t.Fatalf("%d %q", code, out)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("uninstall must delete the file it created")
	}
	if code, out, _ := runSS("uninstall", "--system"); code != 0 || !strings.Contains(out, "was not set up") {
		t.Errorf("again: %d %q", code, out)
	}
}

func TestSystemInstallRejectsOtherArguments(t *testing.T) {
	fakeSystem(t, true)
	if code, _, e := runSS("install", "--system", "--ai"); code != 2 || e == "" {
		t.Errorf("--system --ai: %d %q", code, e)
	}
	if code, _, _ := runSS("uninstall", "--bogus"); code != 2 {
		t.Errorf("uninstall --bogus: %d", code)
	}
}

func TestSystemInstallAndUninstallReportFailures(t *testing.T) {
	dir := fakeSystem(t, true)
	fakeExecutable(t, "", errors.New("gone"))
	if code, _, e := runSS("install", "--system"); code != 1 || !strings.Contains(e, "gone") {
		t.Errorf("no binary: %d %q", code, e)
	}
	fakeExecutable(t, "/usr/bin/safeshell", nil)
	if err := os.MkdirAll(filepath.Join(dir, "etc/profile.d/safeshell.sh"), 0o755); err != nil { // a folder where the file goes
		t.Fatal(err)
	}
	if code, _, e := runSS("install", "--system"); code != 1 || !strings.Contains(e, "safeshell install:") {
		t.Errorf("install: %d %q", code, e)
	}
	if code, _, e := runSS("uninstall", "--system"); code != 1 || !strings.Contains(e, "safeshell uninstall:") {
		t.Errorf("uninstall: %d %q", code, e)
	}
}

func TestFishHookNamesTheBinaryItCalls(t *testing.T) {
	fakeExecutable(t, "/opt/it's/safeshell", nil)
	if code, out, _ := runSS("hook", "fish"); code != 0 || !strings.Contains(out, `'/opt/it\'s/safeshell' $argv`) {
		t.Errorf("%d %q", code, out)
	}
}
