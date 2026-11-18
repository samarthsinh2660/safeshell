package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// snapshotLimit is the most file data one snapshot may copy.
const snapshotLimit = 2 << 30

// snapshotDir is where snapshots live; tests replace it.
var snapshotDir = func() string {
	return filepath.Join(xdg.Dir("XDG_DATA_HOME", ".local", "share"), "snapshots")
}

// targets lists the paths a simulated run changed: files to save, and files it
// created, which undo removes.
func targets(changes []sandbox.Change) []snapshot.Target {
	var ts []snapshot.Target
	for _, c := range changes {
		ts = append(ts, snapshot.Target{Path: c.Abs(), Create: c.Kind == sandbox.Created})
	}
	return ts
}

const teamNoSnapshot = "Your team's policy asks for a snapshot first, but SafeShell could not tell which files this will change, so nothing was saved."

// protect saves what the analysed command is expected to change, just before
// it runs: the files the sandbox saw change, or, with no sandbox result, the
// files its arguments name. It returns a one-line note for the person; "" when
// nothing needed saving.
func protect(a engine.Analysis) string {
	var ts []snapshot.Target
	switch {
	case a.Sim != nil:
		ts = targets(a.Sim.Diff.Changes)
	case a.Level >= domain.R2 || a.Team.Snapshot:
		for _, w := range a.Targets {
			p := w.Path
			if fi, err := os.Stat(p); w.Into != "" && err == nil && fi.IsDir() { // mv or cp into a folder: only the file inside it changes
				p = w.Into
			}
			ts = append(ts, snapshot.Target{Path: p, Create: true})
		}
		if len(ts) == 0 && a.Team.Snapshot {
			return teamNoSnapshot
		}
		if len(ts) == 0 {
			return "No snapshot was taken: SafeShell could not tell which files this will change, so it cannot be undone."
		}
	}
	if len(ts) == 0 {
		return ""
	}
	m, err := snapshot.Create(snapshotDir(), report.MaskSecrets(a.Line), ts, snapshotLimit)
	if err != nil {
		return "No snapshot taken (" + err.Error() + "): this cannot be undone."
	}
	note := fmt.Sprintf("Snapshot saved (%s). Undo with: safeshell undo", report.HumanBytes(m.Bytes))
	if a.TargetsGap {
		note += ". Some files could not be named, so changes to those cannot be undone."
	}
	return note
}

func runSnapshots(stdout, stderr io.Writer) int {
	ms, err := snapshot.List(snapshotDir())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell snapshots:", err)
		return 1
	}
	if len(ms) == 0 {
		fmt.Fprintln(stdout, "No snapshots yet.")
	}
	for _, m := range ms {
		fmt.Fprintf(stdout, "%s  %s  %s\n", m.ID, report.HumanBytes(m.Bytes), report.Printable(m.Line))
	}
	return 0
}

// runUndo restores a snapshot (the newest by default) and checks the result.
func runUndo(args []string, stdout, stderr io.Writer) int {
	store := snapshotDir()
	var id string
	if !unknownOption("undo", args, stderr) {
		return 2
	}
	if len(args) > 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if len(args) == 1 {
		id = args[0]
	} else {
		ms, err := snapshot.List(store)
		if err != nil || len(ms) == 0 {
			fmt.Fprintln(stderr, "safeshell undo: there is nothing to undo.")
			return 1
		}
		id = ms[0].ID
	}
	m, err := snapshot.Load(store, id)
	if errors.Is(err, snapshot.ErrBadID) {
		fmt.Fprintln(stderr, "safeshell undo: that is not a snapshot id. Run 'safeshell snapshots' to see them.")
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "safeshell undo: no such snapshot:", id)
		return 1
	}
	fmt.Fprintf(stdout, "Undoing: %s\n", report.Printable(m.Line))
	r, err := snapshot.Restore(store, id)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell undo:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Restored %s, removed %s that the command created.\n", report.Files(r.Restored), report.Files(r.Removed))
	if len(r.Problems) > 0 {
		fmt.Fprintln(stdout, "NOT fully undone:")
		for _, p := range r.Problems {
			fmt.Fprintln(stdout, "  -", report.Printable(p))
		}
		return 1
	}
	fmt.Fprintln(stdout, "Verified: every file matches what it was before.")
	return 0
}
