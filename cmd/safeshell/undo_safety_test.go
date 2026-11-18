package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

// snap takes a snapshot of files the way the guard does, in the given terminal and folder.
func snap(t *testing.T, session, cwd, line string, files ...string) snapshot.Manifest {
	t.Helper()
	t.Setenv("SAFESHELL_HOOK", session)
	a := sampleAnalysis()
	a.Line = line
	var cs []sandbox.Change
	for _, f := range files {
		cs = append(cs, sandbox.Change{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Modified})
	}
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: cs}}
	if note := protect(a, cwd); !strings.Contains(note, "Snapshot saved") {
		t.Fatal(note)
	}
	ms, _ := snapshot.List(snapshotDir())
	return ms[0]
}

// nextCommand is what the hook does when the person types another line in this terminal.
func nextCommand(t *testing.T) {
	t.Helper()
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	answer(t, "")
	runSS("guard", "--", "ls")
}

func read(t *testing.T, f string) string {
	t.Helper()
	b, _ := os.ReadFile(f)
	return string(b)
}

func TestSecondUndoAsksAndNeverOverwritesNewerWorkWithoutYes(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(f, []byte("v1"), 0o644)
	m := snap(t, "111", "/work", "sed -i s/1/2/ a.txt", f)
	os.WriteFile(f, []byte("version two"), 0o644) // the command runs
	nextCommand(t)                                // the next line seals what the command left

	answer(t, "yes\n")
	code, out, _ := runSS("undo")
	if code != 0 || read(t, f) != "v1" || strings.Contains(out, "WARNING") {
		t.Fatalf("first undo: %d %q %q", code, out, read(t, f))
	}
	os.WriteFile(f, []byte("v3, written after the undo"), 0o644)

	code, _, errOut := runSS("undo") // the default pick skips the used snapshot
	if code != 1 || !strings.Contains(errOut, "already been undone") || read(t, f) != "v3, written after the undo" {
		t.Fatalf("default: %d %q", code, errOut)
	}
	for _, in := range []string{"n\n", "y\n", ""} { // anything but a full yes leaves the newer work alone
		answer(t, in)
		code, out, _ = runSS("undo", m.ID)
		if code != 1 || read(t, f) != "v3, written after the undo" {
			t.Fatalf("%q: %d %q %q", in, code, out, read(t, f))
		}
		for _, want := range []string{"already undone", "WARNING", "overwrite that newer work", f, "Nothing was restored"} {
			if !strings.Contains(out, want) {
				t.Errorf("%q: missing %q in %q", in, want, out)
			}
		}
	}
	answer(t, "yes\n")
	if code, _, _ := runSS("undo", m.ID); code != 0 || read(t, f) != "v1" {
		t.Errorf("yes: %d %q", code, read(t, f))
	}
	os.WriteFile(f, []byte("v4"), 0o644)
	if code, out, _ := runSS("undo", "--yes", m.ID); code != 0 || read(t, f) != "v1" || !strings.Contains(out, "WARNING") {
		t.Errorf("--yes: %d %q", code, out)
	}
}

func TestNewerWorkListIsShortened(t *testing.T) {
	useStore(t)
	d := t.TempDir()
	var files []string
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		files = append(files, filepath.Join(d, n))
		os.WriteFile(files[len(files)-1], []byte("v1"), 0o644)
	}
	snap(t, "5", d, "sed -i x *", files...)
	nextCommand(t)
	for _, f := range files {
		os.WriteFile(f, []byte("newer work"), 0o644)
	}
	answer(t, "n\n")
	if _, out, _ := runSS("undo"); !strings.Contains(out, "7 files changed") || !strings.Contains(out, "and 2 more") {
		t.Errorf("%q", out)
	}
}

func TestUndoNamesTheSnapshotFromThisTerminalOrFolder(t *testing.T) {
	useStore(t)
	d := t.TempDir()
	fa, fb := filepath.Join(d, "a"), filepath.Join(d, "b")
	os.WriteFile(fa, []byte("a1"), 0o644)
	os.WriteFile(fb, []byte("b1"), 0o644)
	snap(t, "111", "/proj/a", "rm a", fa) // terminal A, older
	snap(t, "222", "/proj/b", "rm b", fb) // terminal B, newest
	os.WriteFile(fa, []byte("a2"), 0o644)
	os.WriteFile(fb, []byte("b2"), 0o644)

	// Terminal A is shown its own command, not B's, and nothing changes without a yes.
	t.Setenv("SAFESHELL_HOOK", "111")
	answer(t, "n\n")
	code, out, _ := runSS("undo")
	if code != 1 || !strings.Contains(out, "rm a") || strings.Contains(out, "rm b") || !strings.Contains(out, "folder: /proj/a") ||
		!strings.Contains(out, "saved:  1 file") || read(t, fa) != "a2" || read(t, fb) != "b2" {
		t.Fatalf("terminal A: %d %q", code, out)
	}
	answer(t, "yes\n")
	if code, _, _ := runSS("undo"); code != 0 || read(t, fa) != "a1" || read(t, fb) != "b2" {
		t.Errorf("A restore: %d %q %q", code, read(t, fa), read(t, fb))
	}

	// Without a terminal match the folder decides.
	useStore(t)
	snap(t, "111", d, "rm a", fa)
	snap(t, "222", "/elsewhere", "rm b", fb)
	t.Setenv("SAFESHELL_HOOK", "333")
	t.Chdir(d)
	answer(t, "n\n")
	if _, out, _ := runSS("undo"); !strings.Contains(out, "rm a") {
		t.Errorf("folder: %q", out)
	}

	// Otherwise the newest overall.
	t.Chdir(t.TempDir())
	answer(t, "n\n")
	if _, out, _ := runSS("undo"); !strings.Contains(out, "rm b") {
		t.Errorf("newest: %q", out)
	}
}

func TestUndoWithNobodyToAskRestoresNothingAndSaysHow(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("v1"), 0o644)
	m := snap(t, "1", "/w", "rm a", f)
	os.WriteFile(f, []byte("v2"), 0o644)
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	guardInput = func() io.Reader { r, _ := os.Open(f); return r } // a file, not a terminal
	code, out, _ := runSS("undo")
	if code != 1 || read(t, f) != "v2" || !strings.Contains(out, "Nothing was restored") || !strings.Contains(out, "safeshell undo "+m.ID+" --yes") {
		t.Fatalf("%d %q", code, out)
	}
	if code, _, _ := runSS("undo", "--yes"); code != 0 || read(t, f) != "v1" { // --yes needs no one to ask
		t.Errorf("--yes: %d %q", code, read(t, f))
	}
}

func TestUndoStillRestoresOldSnapshotsWithoutOrigin(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("v1"), 0o644)
	if _, err := snapshot.Create(snapshotDir(), "rm a", []snapshot.Target{{Path: f}}, 0); err != nil {
		t.Fatal(err)
	}
	os.Remove(f)
	answer(t, "yes\n")
	code, out, _ := runSS("undo")
	if code != 0 || read(t, f) != "v1" || strings.Contains(out, "folder:") || strings.Contains(out, "WARNING") {
		t.Errorf("%d %q", code, out)
	}
}

func TestUndoSaysWhenItCannotMarkTheSnapshotUsed(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	useStore(t)
	f := filepath.Join(t.TempDir(), "a")
	os.WriteFile(f, []byte("v1"), 0o644)
	m := snap(t, "1", "/w", "rm a", f)
	os.WriteFile(f, []byte("v2"), 0o644)
	os.Chmod(filepath.Join(snapshotDir(), m.ID, "manifest.json"), 0o400)
	code, out, _ := runSS("undo", "--yes")
	if code != 0 || read(t, f) != "v1" || !strings.Contains(out, "could not mark this snapshot as undone") {
		t.Errorf("%d %q", code, out)
	}
}
