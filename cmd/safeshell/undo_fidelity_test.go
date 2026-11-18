package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

func TestProtectAndUndoSayWhatCouldNotBeSaved(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	useStore(t)
	d := filepath.Join(t.TempDir(), "proj")
	locked := filepath.Join(d, "locked")
	os.MkdirAll(locked, 0o755)
	os.WriteFile(filepath.Join(d, "a.txt"), []byte("alpha"), 0o644)
	if err := syscall.Mkfifo(filepath.Join(d, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chmod(locked, 0o000)
	defer os.Chmod(locked, 0o755)

	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: strings.TrimPrefix(d, "/"), Kind: sandbox.Deleted}}}}
	fakeAnalyze(t, a)
	answer(t, "d\nyes\n")
	code, _, errOut := runSS("guard", "--", "rm -rf proj")
	if code != 0 || !strings.Contains(errOut, "except 1 item that could not be read, so undo cannot bring them back: "+locked+" (permission denied)") {
		t.Fatalf("code %d err %q", code, errOut)
	}
	os.Chmod(locked, 0o755)
	os.RemoveAll(d) // the command ran
	answer(t, "yes\n")
	code, out, _ := runSS("undo")
	if code != 0 || !strings.Contains(out, "not saved: 1 item that could not be read") || !strings.Contains(out, "The items that could not be read were not saved") {
		t.Fatalf("undo: %d %q", code, out)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "a.txt")); string(b) != "alpha" {
		t.Error("readable file not restored")
	}
	if fi, err := os.Lstat(filepath.Join(d, "pipe")); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Error("pipe not recreated")
	}
}

func TestLeftOutTextListsAFewAndCountsTheRest(t *testing.T) {
	var l []snapshot.LeftOut
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e"} {
		l = append(l, snapshot.LeftOut{Path: p, Reason: "permission denied"})
	}
	want := "5 items that could not be read, so undo cannot bring them back: /a (permission denied), /b (permission denied), /c (permission denied), and 2 more"
	if got := leftOutText(l); got != want {
		t.Errorf("%q", got)
	}
}

func TestUndoReportsNewFilesInASavedFolder(t *testing.T) {
	useStore(t)
	d := t.TempDir()
	os.WriteFile(filepath.Join(d, "a"), []byte("v1"), 0o644)
	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: strings.TrimPrefix(d, "/"), Kind: sandbox.Modified}}}}
	fakeAnalyze(t, a)
	answer(t, "d\nyes\n")
	runSS("guard", "--", "x")
	os.WriteFile(filepath.Join(d, "new"), []byte("n"), 0o644)
	answer(t, "yes\n")
	code, out, _ := runSS("undo")
	if code != 1 || !strings.Contains(out, "NOT fully undone") || !strings.Contains(out, "new is new") {
		t.Errorf("%d %q", code, out)
	}
}
