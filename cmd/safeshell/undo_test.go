package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/classify/rules"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

func useStore(t *testing.T) {
	t.Helper()
	old := snapshotDir
	d := t.TempDir()
	snapshotDir = func() string { return d }
	t.Cleanup(func() { snapshotDir = old })
}

func TestGuardSnapshotsThenUndoRestores(t *testing.T) {
	useStore(t)
	d := t.TempDir()
	f := filepath.Join(d, "keep.txt")
	os.WriteFile(f, []byte("precious"), 0o644)
	made := filepath.Join(d, "made.txt")

	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{
		{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Deleted},
		{Path: strings.TrimPrefix(made, "/"), Kind: sandbox.Created},
	}}}
	fakeAnalyze(t, a)
	answer(t, "d\nyes\n")
	code, _, errOut := runSS("guard", "--", "rm -rf ~/projects")
	if code != 0 || !strings.Contains(errOut, "Snapshot saved") {
		t.Fatalf("code %d err %q", code, errOut)
	}

	os.Remove(f) // the approved command runs
	os.WriteFile(made, []byte("x"), 0o644)

	answer(t, "yes\n")
	code, out, errOut := runSS("undo")
	if code != 0 || !strings.Contains(out, "Verified") {
		t.Fatalf("undo: %d %q %q", code, out, errOut)
	}
	if b, _ := os.ReadFile(f); string(b) != "precious" {
		t.Error("file not restored")
	}
	if _, err := os.Stat(made); err == nil {
		t.Error("created file not removed")
	}
	if _, out, _ := runSS("snapshots"); !strings.Contains(out, "rm -rf ~/projects") {
		t.Errorf("snapshots: %q", out)
	}
}

func TestNoSnapshotWhenCancelledReplacedOrNothingChanges(t *testing.T) {
	useStore(t)
	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: "tmp/x", Kind: sandbox.Deleted}}}}
	for _, in := range []string{"c\n", "1\n"} {
		fakeAnalyze(t, a)
		answer(t, in)
		runSS("guard", "--", "rm -rf ~/projects")
	}
	a.Sim = &sandbox.Result{}
	a.Decision = domain.Confirm
	fakeAnalyze(t, a)
	answer(t, "y\n")
	runSS("guard", "--", "touch x")
	if _, out, _ := runSS("snapshots"); !strings.Contains(out, "No snapshots") {
		t.Errorf("unexpected snapshots: %q", out)
	}
}

func TestUndoErrors(t *testing.T) {
	useStore(t)
	if code, _, errOut := runSS("undo"); code != 1 || !strings.Contains(errOut, "nothing to undo") {
		t.Errorf("%d %q", code, errOut)
	}
	if code, _, errOut := runSS("undo", "20200101T000000.000000000Z"); code != 1 || !strings.Contains(errOut, "no such snapshot") {
		t.Errorf("unknown id: %d %q", code, errOut)
	}
	if code, _, errOut := runSS("undo", "../../x"); code != 1 || !strings.Contains(errOut, "not a snapshot id") {
		t.Errorf("bad id: %d %q", code, errOut)
	}
	if code, _, _ := runSS("undo", "a", "b"); code != 2 {
		t.Errorf("usage: %d", code)
	}
}

func TestUndoReportsWhatCouldNotBeRestored(t *testing.T) {
	useStore(t)
	d := t.TempDir()
	f := filepath.Join(d, "a")
	os.WriteFile(f, []byte("v1"), 0o644)
	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Modified}}}}
	fakeAnalyze(t, a)
	answer(t, "d\nyes\n")
	runSS("guard", "--", "x")
	os.Remove(f)
	os.Chmod(d, 0o500) // the folder became read-only, so the file cannot come back
	defer os.Chmod(d, 0o700)
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	answer(t, "yes\n")
	code, out, _ := runSS("undo")
	if code != 1 || !strings.Contains(out, "NOT fully undone") {
		t.Errorf("%d %q", code, out)
	}
}

func TestSnapshotDirFollowsXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got := snapshotDir(); got != "/data/safeshell/snapshots" {
		t.Error(got)
	}
}

func TestSnapshotManifestMasksSecrets(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "x.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	a := sampleAnalysis()
	a.Line = "API_KEY=abc123 rm x.txt"
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Deleted}}}}
	protect(a, "/work")
	ms, _ := snapshot.List(snapshotDir())
	if len(ms) != 1 || ms[0].Line != "API_KEY=*** rm x.txt" {
		t.Errorf("manifests %+v", ms)
	}
}

// noSandbox makes every simulation fail, as on a machine without user namespaces.
func noSandbox(t *testing.T) {
	t.Helper()
	fakeSim(t, sandbox.Result{}, errors.New("no user namespaces"))
}

func TestWithoutSandboxTheNamedFileIsSnapshottedAndUndoRestoresIt(t *testing.T) {
	useStore(t)
	noSandbox(t)
	t.Setenv("SAFESHELL_NODAEMON", "1")
	d := t.TempDir()
	t.Chdir(d)
	f := filepath.Join(d, "notes.txt")
	os.WriteFile(f, []byte("precious"), 0o644)
	answer(t, "y\n")
	code, _, errOut := runSS("guard", "--", "rm notes.txt")
	if code != 0 || !strings.Contains(errOut, "Snapshot saved") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	os.Remove(f)
	if code, out, _ := runSS("undo"); code != 0 || !strings.Contains(out, "Verified") {
		t.Fatalf("undo %d %q", code, out)
	}
	if b, _ := os.ReadFile(f); string(b) != "precious" {
		t.Errorf("restored %q", b)
	}
}

func TestWithoutSandboxAnUnknownTargetWarnsThereIsNoUndo(t *testing.T) {
	useStore(t)
	noSandbox(t)
	t.Setenv("SAFESHELL_NODAEMON", "1")
	t.Chdir(t.TempDir())
	answer(t, "y\n")
	_, _, errOut := runSS("guard", "--", "rm *.txt")
	if !strings.Contains(errOut, "No snapshot was taken") || !strings.Contains(errOut, "cannot be undone") {
		t.Errorf("err %q", errOut)
	}
}

func TestProtectNamesGapsAndFailures(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("x"), 0o644)
	a := sampleAnalysis()
	a.Targets, a.TargetsGap = []rules.Written{{Path: f}}, true
	if got := protect(a, ""); !strings.Contains(got, "Snapshot saved") || !strings.Contains(got, "could not be named") {
		t.Error(got)
	}
	snapshotDir = func() string { return "/proc/none" }
	if got := protect(a, ""); !strings.Contains(got, "No snapshot taken") {
		t.Error(got)
	}
	a.Level, a.Targets = domain.R1, nil
	if got := protect(a, ""); got != "" {
		t.Error("a harmless command needs no snapshot, got", got)
	}
}

// mv or cp into an existing folder changes one file inside it, so only that
// file is saved, not the whole folder.
func TestProtectSavesOnlyTheFileMovedIntoAFolder(t *testing.T) {
	useStore(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "big"), make([]byte, 4096), 0o644)
	a := sampleAnalysis()
	a.Targets = []rules.Written{{Path: dir, Into: filepath.Join(dir, "a")}}
	if got := protect(a, ""); !strings.Contains(got, "Snapshot saved (0 B)") {
		t.Errorf("only dir/a (absent) is saved, not the folder: %s", got)
	}
	a.Targets = []rules.Written{{Path: filepath.Join(dir, "big"), Into: filepath.Join(dir, "big", "a")}}
	if got := protect(a, ""); !strings.Contains(got, "Snapshot saved (4.0 KiB)") {
		t.Errorf("a file destination is saved itself: %s", got)
	}
}

// With a btrfs subvolume under the files, protect takes a native snapshot and
// says nothing was copied; undo restores from it.
func TestProtectUsesNativeSnapshotWhenItCoversTheFiles(t *testing.T) {
	useStore(t)
	root := t.TempDir()
	f := filepath.Join(root, "a.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	h := snapshot.OS()
	h.Statfs = func(string) (snapshot.Fs, error) { return snapshot.Fs{Type: 0x9123683E}, nil }
	ino := h.Ino
	h.Ino = func(p string) (uint64, error) {
		if p == root {
			return 256, nil
		}
		return ino(p)
	}
	h.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		return exec.Command("cp", "-a", root, args[len(args)-1]).CombinedOutput()
	}
	old := snapshotHost
	t.Cleanup(func() { snapshotHost = old })
	snapshotHost = func() snapshot.Host { return h }

	a := sampleAnalysis()
	a.Targets = []rules.Written{{Path: f}}
	if got := protect(a, ""); !strings.Contains(got, "Snapshot saved (btrfs, nothing copied)") {
		t.Fatalf("note: %s", got)
	}
	os.Remove(f)
	var out, errOut strings.Builder
	if code := runUndo([]string{"--yes"}, &out, &errOut); code != 0 {
		t.Fatalf("undo: %d %s", code, errOut.String())
	}
	if b, _ := os.ReadFile(f); string(b) != "hello" {
		t.Errorf("not restored: %q", b)
	}
}
