package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

func TestDeactivateAndActivate(t *testing.T) {
	home := doctorHome(t)
	off := filepath.Join(home, "state", "safeshell", "off")

	if code, out, _ := runSS("deactivate"); code != 0 || !strings.Contains(out, "SafeShell is off in every terminal") || !strings.Contains(out, "safeshell activate") {
		t.Errorf("deactivate: %d %q", code, out)
	}
	if _, err := os.Stat(off); err != nil {
		t.Fatal("deactivate must create the off file the hooks look for")
	}
	if code, out, _ := runSS("deactivate"); code != 0 || !strings.Contains(out, "already off") {
		t.Errorf("deactivate twice: %d %q", code, out)
	}
	if code, out, _ := runSS("activate"); code != 0 || !strings.Contains(out, "SafeShell is on. Every command is checked again.") {
		t.Errorf("activate: %d %q", code, out)
	}
	if _, err := os.Stat(off); err == nil {
		t.Error("activate must remove the off file")
	}
	if code, out, _ := runSS("activate"); code != 0 || !strings.Contains(out, "SafeShell is on") {
		t.Errorf("activate twice: %d %q", code, out)
	}
}

func TestActivateSaysWhenTheHookIsNotInstalled(t *testing.T) {
	doctorHome(t)
	_, out, _ := runSS("activate")
	if !strings.Contains(out, "hook is not installed") || !strings.Contains(out, "Run: safeshell install") {
		t.Errorf("%q", out)
	}
	if code, _, _ := runSS("install"); code != 0 {
		t.Fatal("install failed")
	}
	if _, out, _ := runSS("activate"); strings.Contains(out, "not installed") {
		t.Errorf("the hook is installed: %q", out)
	}
	t.Setenv("SHELL", "/usr/bin/nu")
	if _, out, _ := runSS("activate"); !strings.Contains(out, "not installed") {
		t.Errorf("an unsupported shell has no hook: %q", out)
	}
}

func TestPauseFailuresSayWhatToDo(t *testing.T) {
	home := doctorHome(t)
	state := filepath.Join(home, "state")
	if err := os.WriteFile(state, nil, 0o600); err != nil { // a file where the folder should be
		t.Fatal(err)
	}
	if code, _, e := runSS("deactivate"); code != 1 || !strings.Contains(e, "cannot save the setting") {
		t.Errorf("%d %q", code, e)
	}
	if err := os.Remove(state); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(state, "safeshell", "off", "x"), 0o700); err != nil { // an off that cannot be removed
		t.Fatal(err)
	}
	if code, _, e := runSS("activate"); code != 1 || !strings.Contains(e, "Delete that file by hand") {
		t.Errorf("%d %q", code, e)
	}
}

func TestDoctorSaysWhenPaused(t *testing.T) {
	doctorHome(t)
	t.Setenv("SAFESHELL_HOOK", "1")
	runSS("deactivate")
	wantAll(t, doctorOut(t), "MISSING  hook active here", "paused: run safeshell activate")
	runSS("activate")
	if out := doctorOut(t); strings.Contains(out, "paused") {
		t.Errorf("not paused any more:\n%s", out)
	}
}

func TestDeactivateIsRefusedInStrictModeButActivateWorks(t *testing.T) {
	home := doctorHome(t)
	off := filepath.Join(home, "state", "safeshell", "off")
	old := teamFile
	defer func() { teamFile = old }()
	teamFile = func() (policy.File, bool, error) { return policy.File{Strict: true}, true, nil }
	if code, out, e := runSS("deactivate"); code != 1 || out != "" || !strings.Contains(e, "strict mode is on") {
		t.Errorf("deactivate: %d %q %q", code, out, e)
	}
	if _, err := os.Stat(off); err == nil {
		t.Fatal("deactivate created the off file in strict mode")
	}
	_ = os.MkdirAll(filepath.Dir(off), 0o700)
	_ = os.WriteFile(off, nil, 0o600)
	if code, _, _ := runSS("activate"); code != 0 {
		t.Error("activate must still work")
	}
	if _, err := os.Stat(off); err == nil {
		t.Error("activate did not remove the off file")
	}
}
