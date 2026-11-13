package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

func tourEnv(t *testing.T) (tmp string) {
	t.Helper()
	tmp = t.TempDir()
	t.Setenv("TMPDIR", tmp)
	oldSeen, oldTerm, oldRestore := tourSeenPath, onTerminal, tourRestore
	t.Cleanup(func() { tourSeenPath, onTerminal, tourRestore = oldSeen, oldTerm, oldRestore })
	seen := filepath.Join(t.TempDir(), "state", "tour-seen")
	tourSeenPath = func() string { return seen }
	return tmp
}

func TestTourWalksThroughAndLeavesNothingBehind(t *testing.T) {
	tmp := tourEnv(t)
	code, out, errOut := runSS("tour")
	if code != 0 || errOut != "" {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	wantAll(t, out,
		"Step 1 of 4", "R0 Read-only", "$ ls",
		"Step 2 of 4", "R3 Irreversible", "$ rm -rf project", "Nothing is run in this tour",
		"Step 3 of 4", "Snapshot saved", "notes.txt was deleted",
		"Step 4 of 4", "Verified: every file matches", "safeshell help start")
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("the tour left files behind: %v", left)
	}
	if _, err := os.Stat(tourSeenPath()); err != nil {
		t.Error("running the tour must count as seen")
	}
}

func TestTourFailuresSayWhatToDo(t *testing.T) {
	tmp := tourEnv(t)
	t.Setenv("TMPDIR", filepath.Join(tmp, "missing"))
	if code, _, e := runSS("tour"); code != 1 || !strings.Contains(e, "Check that TMPDIR") {
		t.Errorf("no temp folder: %d %q", code, e)
	}

	t.Setenv("TMPDIR", tmp)
	tourRestore = func(string, string) (snapshot.Report, error) { return snapshot.Report{}, os.ErrPermission }
	if code, _, e := runSS("tour"); code != 1 || !strings.Contains(e, "safeshell tour:") {
		t.Errorf("restore error: %d %q", code, e)
	}
	tourRestore = func(string, string) (snapshot.Report, error) {
		return snapshot.Report{Problems: []string{"x"}}, nil
	}
	if code, _, e := runSS("tour"); code != 1 || !strings.Contains(e, "could not be restored exactly") {
		t.Errorf("restore problems: %d %q", code, e)
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a failed tour left files behind: %v", left)
	}
}

func TestTourStepsFailWhenTheFolderCannotBeUsed(t *testing.T) {
	tourEnv(t)
	d := t.TempDir()
	if err := tourSteps(io.Discard, filepath.Join(d, "missing"), false); err == nil {
		t.Error("a missing folder must be an error")
	}
	if err := os.WriteFile(filepath.Join(d, "snapshots"), nil, 0o600); err != nil { // blocks the snapshot store
		t.Fatal(err)
	}
	if err := tourSteps(io.Discard, d, false); err == nil {
		t.Error("an unusable snapshot store must be an error")
	}
}

func TestTourRejectsOptions(t *testing.T) {
	if code, _, e := runSS("tour", "x"); code != 2 || !strings.Contains(e, "takes no options") {
		t.Errorf("%d %q", code, e)
	}
}

func TestTourHintIsShownOnceAndOnlyInATerminal(t *testing.T) {
	tourEnv(t)
	var b strings.Builder
	tourHint(&b) // stderr here is a buffer, not a terminal
	if b.Len() != 0 {
		t.Errorf("shown outside a terminal: %q", b.String())
	}
	if _, err := os.Stat(tourSeenPath()); err == nil {
		t.Error("nothing is recorded outside a terminal")
	}

	onTerminal = func(io.Writer) bool { return true }
	tourHint(&b)
	if !strings.Contains(b.String(), "safeshell tour") {
		t.Errorf("first run: %q", b.String())
	}
	n := b.Len()
	tourHint(&b)
	if b.Len() != n {
		t.Error("the hint was shown twice")
	}
}

func TestTourHintIsSkippedWhenItCannotBeRemembered(t *testing.T) {
	tourEnv(t)
	onTerminal = func(io.Writer) bool { return true }
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	tourSeenPath = func() string { return filepath.Join(blocker, "tour-seen") }
	var b strings.Builder
	tourHint(&b)
	if b.Len() != 0 {
		t.Errorf("a hint that would repeat every command must not be shown: %q", b.String())
	}
}

func TestGuardMentionsTheTourOnceInATerminal(t *testing.T) {
	tourEnv(t)
	onTerminal = func(io.Writer) bool { return true }
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	answer(t, "")
	_, out, e := runSS("guard", "--", "ls")
	if out != "ls\n" {
		t.Errorf("the tour hint must not touch stdout: %q", out)
	}
	if !strings.Contains(e, "safeshell tour") {
		t.Errorf("first command: %q", e)
	}
	_, _, e = runSS("guard", "--", "ls")
	if strings.Contains(e, "safeshell tour") {
		t.Errorf("second command: %q", e)
	}
}

func TestDefaultTourSeenPathIsUnderTheStateFolder(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	if got := defaultTourSeenPath(); got != filepath.Join(state, "safeshell", "tour-seen") {
		t.Errorf("%q", got)
	}
}

func TestIsTerminal(t *testing.T) {
	tourEnv(t)
	if onTerminal(io.Discard) {
		t.Error("a plain writer is not a terminal")
	}
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip("no /dev/null")
	}
	defer f.Close()
	if !onTerminal(f) {
		t.Error("/dev/null is a character device, as a terminal is")
	}
	d, _ := os.Open(t.TempDir())
	defer d.Close()
	if onTerminal(d) {
		t.Error("a folder is not a terminal")
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	w.Close()
	if onTerminal(w) {
		t.Error("a closed file cannot be stat-ed as a terminal")
	}
}
