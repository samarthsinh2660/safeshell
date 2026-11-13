package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// Test seams: tests replace these. tourRestore stands in for the real restore;
// onTerminal says whether a writer is a terminal (unlike isTerminal, a plain writer is not).
var (
	tourRestore = snapshot.Restore
	onTerminal  = func(w io.Writer) bool {
		f, ok := w.(*os.File)
		if !ok {
			return false
		}
		st, err := f.Stat()
		return err == nil && st.Mode()&os.ModeCharDevice != 0
	}
)

// tourSeenPath is the file whose existence means the tour hint was shown; tests replace it.
var tourSeenPath = defaultTourSeenPath

func defaultTourSeenPath() string {
	return filepath.Join(xdg.Dir("XDG_STATE_HOME", ".local", "state"), "tour-seen")
}

// tourHint tells a new person about the tour, once, and only in a terminal.
// It never runs the tour, so the shell hook is never held up. If the "seen"
// file cannot be written the hint is skipped, so it can never repeat.
func tourHint(stderr io.Writer) {
	if !onTerminal(stderr) {
		return
	}
	p := tourSeenPath()
	if _, err := os.Stat(p); err == nil {
		return
	}
	if markTourSeen(p) != nil {
		return
	}
	fmt.Fprintln(stderr, "SafeShell is checking your commands. New here? Run  safeshell tour  for a one-minute walk-through (shown once).")
}

func markTourSeen(p string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, nil, 0o600)
}

// runTour is `safeshell tour`: an R0 command, a blocked rm -rf, a snapshot and
// an undo, in a throwaway folder that is removed at the end.
func runTour(stdout, stderr io.Writer, color bool) int {
	_ = markTourSeen(tourSeenPath()) // a person who ran the tour needs no hint
	tmp, err := os.MkdirTemp("", "safeshell-tour-")
	if err != nil {
		fmt.Fprintln(stderr, "safeshell tour: cannot make a temporary folder:", err)
		fmt.Fprintln(stderr, "Check that TMPDIR is a folder you can write to.")
		return 1
	}
	defer os.RemoveAll(tmp)
	fmt.Fprintln(stdout, "SafeShell tour. Everything happens in a throwaway folder that is deleted at the end.")
	if err := tourSteps(stdout, tmp, color); err != nil {
		fmt.Fprintln(stderr, "safeshell tour:", err)
		return 1
	}
	fmt.Fprintln(stdout, "\nThat is the idea. The throwaway folder is gone. Next: safeshell help start")
	return 0
}

func tourSteps(w io.Writer, dir string, color bool) error {
	c := newPalette(color)
	show := func(n int, what, line string) {
		fmt.Fprintf(w, "\n%sStep %d of 4: %s%s\n", c.bold, n, what, c.reset)
		printCheck(w, line, classify.Classify(line), color)
	}
	file := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(file, []byte("my notes\n"), 0o600); err != nil {
		return err
	}
	show(1, "a command that only reads runs without a fuss.", "ls")
	show(2, "a command that cannot be undone is stopped. Nothing is run in this tour.", "rm -rf project")
	show(3, "a risky command that can be undone gets a snapshot first.", "rm notes.txt")
	m, err := snapshot.Create(filepath.Join(dir, "snapshots"), "rm notes.txt", []snapshot.Target{{Path: file}}, snapshotLimit)
	if err == nil {
		err = os.Remove(file)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "\n  Snapshot saved (%s) and notes.txt was deleted.\n", report.HumanBytes(m.Bytes))
	fmt.Fprintf(w, "\n%sStep 4 of 4: undo puts it back and checks it.%s\n  $ safeshell undo\n", c.bold, c.reset)
	r, err := tourRestore(filepath.Join(dir, "snapshots"), m.ID)
	if err == nil && len(r.Problems) > 0 {
		err = errors.New("the file could not be restored exactly")
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "  Restored %s. Verified: every file matches what it was before.\n", report.Files(r.Restored))
	return nil
}
