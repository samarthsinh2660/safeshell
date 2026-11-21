package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

func TestUndoSaysWhenTheSnapshotVanishesBeforeRestoring(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "keep.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	m, _ := snapshot.CreateAt(snapshotDir(), snapshot.Origin{}, "rm keep.txt", []snapshot.Target{{Path: f}}, 0)
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	guardInput = func() io.Reader { // reading the answer is the moment the manifest disappears
		os.Remove(filepath.Join(snapshotDir(), m.ID, "manifest.json"))
		return strings.NewReader("yes\n")
	}
	if code, _, errOut := runSS("undo", m.ID); code != 1 || !strings.Contains(errOut, "safeshell undo:") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestUndoNotesWhenItCannotMarkThePlanDone(t *testing.T) {
	useStore(t)
	units(t, map[string]string{"is-active": "active", "is-enabled": "enabled"})
	guardAllowed(t, "systemctl stop nginx")
	ms, _ := snapshot.List(snapshotDir())
	old := runCommand
	t.Cleanup(func() { runCommand = old })
	runCommand = func(string, string, io.Writer, io.Writer) error {
		return os.Remove(filepath.Join(snapshotDir(), ms[0].ID, "manifest.json"))
	}
	fakeAnalyze(t, plainAllow("sudo systemctl start nginx"))
	answerShared(t, "yes\ny\n")
	if code, out, _ := runSS("undo"); code != 0 || !strings.Contains(out, "could not mark this snapshot as undone") {
		t.Errorf("%d %q", code, out)
	}
}
