package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

// doctorHome gives doctor an empty home, no hook marker and no API keys.
func doctorHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("SAFESHELL_OFF", "")
	t.Setenv("SAFESHELL_HOOK", "")
	t.Setenv("SAFESHELL_AI", "")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(k, "")
	}
	old := probe
	t.Cleanup(func() { probe = old })
	probe = func() platform.Report { return platform.Report{Kernel: "6.8.0", UserNS: true, Overlay: true} }
	return home
}

func doctorOut(t *testing.T) string {
	t.Helper()
	_, out, _ := runSS("doctor")
	return out
}

func wantAll(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in:\n%s", w, out)
		}
	}
}

func TestDoctorHookNotInstalledNorActive(t *testing.T) {
	doctorHome(t)
	out := doctorOut(t)
	wantAll(t, out,
		"MISSING  hook installed",
		"not in ", ".bashrc. Run: safeshell install",
		"MISSING  hook active here",
		"Run  safeshell install  and open a new terminal")
}

func TestDoctorHookInstalledButNotActiveHere(t *testing.T) {
	home := doctorHome(t)
	if code, _, _ := runSS("install"); code != 0 {
		t.Fatal("install failed")
	}
	out := doctorOut(t)
	wantAll(t, out, "ok       hook installed", "in "+filepath.Join(home, ".bashrc"),
		"MISSING  hook active here", "Open a new terminal, or run: source "+filepath.Join(home, ".bashrc"))
}

func TestDoctorHookActive(t *testing.T) {
	doctorHome(t)
	t.Setenv("SAFESHELL_HOOK", strconv.Itoa(os.Getppid()))
	wantAll(t, doctorOut(t), "ok       hook active here", "this terminal checks every command")
}

func TestDoctorHookSwitchedOff(t *testing.T) {
	doctorHome(t)
	t.Setenv("SAFESHELL_HOOK", strconv.Itoa(os.Getppid()))
	t.Setenv("SAFESHELL_OFF", "1")
	wantAll(t, doctorOut(t), "MISSING  hook active here", "SAFESHELL_OFF is set", "Run: unset SAFESHELL_OFF")
}

// A child shell (bash --norc, sh) inherits the variable but is not the shell that has the hook.
func TestDoctorHookInheritedByAChildShellIsNotActive(t *testing.T) {
	doctorHome(t)
	t.Setenv("SAFESHELL_HOOK", strconv.Itoa(os.Getppid()+1))
	wantAll(t, doctorOut(t), "MISSING  hook active here", "not active in this terminal")
}

func TestDoctorUnsupportedShell(t *testing.T) {
	doctorHome(t)
	t.Setenv("SHELL", "/usr/bin/fish")
	out := doctorOut(t)
	wantAll(t, out, "MISSING  hook installed", "only bash and zsh are supported", "Switch to bash or zsh",
		"MISSING  hook active here")
	if strings.Contains(out, "safeshell hook bash") || strings.Contains(out, "Run: safeshell install") || strings.Contains(out, "Run  safeshell install") {
		t.Errorf("an unsupported shell must not be told to install:\n%s", out)
	}
}

func TestDoctorDaemon(t *testing.T) {
	doctorHome(t)
	wantAll(t, doctorOut(t), "info     daemon", "not running (optional). For faster checks run: safeshell daemon")

	withDaemon(t, sampleAnalysis(), nil)
	wantAll(t, doctorOut(t), "ok       daemon", "running (stop it: safeshell daemon stop)")

	fake := filepath.Join(privateDir(t), "fake.sock")
	if err := os.WriteFile(fake, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	socketPath = func() string { return fake }
	wantAll(t, doctorOut(t), "MISSING  daemon", fake+" is not your SafeShell daemon", "Delete that file")

	socketPath = func() string { return "" }
	wantAll(t, doctorOut(t), "info     daemon", "XDG_RUNTIME_DIR is not set to a full path")
}

func TestDoctorSnapshots(t *testing.T) {
	home := doctorHome(t)
	wantAll(t, doctorOut(t), "snapshots", "0 saved, 0 B", "safeshell snapshots")

	f := filepath.Join(home, "a.txt")
	if err := os.WriteFile(f, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Create(snapshotDir(), "rm a.txt", []snapshot.Target{{Path: f}}, 0); err != nil {
		t.Fatal(err)
	}
	wantAll(t, doctorOut(t), "1 saved, 5 B")

	old := snapshotDir
	t.Cleanup(func() { snapshotDir = old })
	snapshotDir = func() string { return f } // a file, so the store cannot be read
	wantAll(t, doctorOut(t), "MISSING  snapshots", "cannot read "+f, "Check that folder's permissions")
}

func TestDoctorAIAndCloud(t *testing.T) {
	doctorHome(t)
	wantAll(t, doctorOut(t), "AI in use", "info     AI now", "off (mode auto)", "cloud provider", "none chosen", "/models")

	useAI(t, fakeAI{}, nil)
	wantAll(t, doctorOut(t), "ok       AI now", "fake (mode auto)")

	useAI(t, nil, os.ErrInvalid)
	wantAll(t, doctorOut(t), "MISSING  AI now", os.ErrInvalid.Error())

	c, _ := config.Load(config.Path())
	c.AI.Cloud.Provider = "anthropic"
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	wantAll(t, doctorOut(t), "Claude, model claude-haiku-5-5", "MISSING  cloud key", "no key for Claude", "ANTHROPIC_API_KEY")

	c.AI.Cloud.Model = "my-model"
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret-value")
	out := doctorOut(t)
	wantAll(t, out, "Claude, model my-model", "ok       cloud key", "set for Claude")
	if strings.Contains(out, "sk-secret-value") {
		t.Error("doctor printed the key")
	}
}

func TestDoctorAIReadiness(t *testing.T) {
	doctorHome(t)
	useAI(t, fakeAI{}, nil) // backend() returns a local backend even when nothing is downloaded
	t.Setenv("SAFESHELL_AI", "local")
	wantAll(t, doctorOut(t), "MISSING  AI now", "not installed. Run: safeshell ai setup")

	t.Setenv("SAFESHELL_AI", "hybrid")
	wantAll(t, doctorOut(t), "MISSING  AI now", "Run: safeshell ai setup")
	c, _ := config.Load(config.Path())
	c.AI.Cloud.Provider = "gemini"
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GEMINI_API_KEY", "k")
	wantAll(t, doctorOut(t), "ok       AI now")
}

func TestDoctorMissingCloudKeyIsReportedOnce(t *testing.T) {
	doctorHome(t)
	useAI(t, nil, errors.New("no API key for Claude: type /models"))
	t.Setenv("SAFESHELL_AI", "cloud")
	out := doctorOut(t)
	wantAll(t, out, "MISSING  AI now", "no API key for Claude")
	if strings.Contains(out, "cloud key") || strings.Count(out, "no API key") != 1 {
		t.Errorf("the missing key must be said once:\n%s", out)
	}
}

func TestDoctorBrokenSettingsAreReportedOnce(t *testing.T) {
	doctorHome(t)
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := doctorOut(t)
	wantAll(t, out, "AI settings are broken")
	if strings.Contains(out, "AI in use") {
		t.Errorf("no AI rows when the settings cannot be read:\n%s", out)
	}
}
