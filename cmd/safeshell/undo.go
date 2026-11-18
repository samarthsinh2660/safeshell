package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
func protect(a engine.Analysis, cwd string) string {
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
	m, err := snapshot.CreateAt(snapshotDir(), snapshot.Origin{Dir: cwd, Session: os.Getenv("SAFESHELL_HOOK")}, report.MaskSecrets(a.Line), ts, snapshotLimit)
	if err != nil {
		return "No snapshot taken (" + err.Error() + "): this cannot be undone."
	}
	note := fmt.Sprintf("Snapshot saved (%s). Undo with: safeshell undo", report.HumanBytes(m.Bytes))
	if len(m.LeftOut) > 0 {
		note = fmt.Sprintf("Snapshot saved (%s), except %s. Undo with: safeshell undo", report.HumanBytes(m.Bytes), leftOutText(m.LeftOut))
	}
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

// runUndo restores a snapshot after showing which one and asking. With no id
// it offers the newest from this terminal, else this folder, else any.
func runUndo(args []string, stdout, stderr io.Writer) int {
	store := snapshotDir()
	var id string
	yes := false
	for len(args) > 0 && args[0] == "--yes" {
		yes, args = true, args[1:]
	}
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
		pick, ok := snapshot.Pick(ms, snapshot.Origin{Dir: cwdOr(""), Session: os.Getenv("SAFESHELL_HOOK")})
		if !ok {
			fmt.Fprintln(stderr, "safeshell undo: everything saved has already been undone. To restore one again, run  safeshell snapshots  and then  safeshell undo ID --yes")
			return 1
		}
		id = pick.ID
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
	newer := snapshot.Changed(m)
	describeUndo(stdout, m, newer)
	if !yes {
		in := guardInput()
		if !isTerminal(in) {
			fmt.Fprintf(stdout, "Nothing was restored, because there is no one to ask. To restore it, run:  safeshell undo %s --yes\n", m.ID)
			return 1
		}
		fmt.Fprint(stdout, "Restore it? Type yes to continue: ")
		if ans, _ := readLine(in); !strings.EqualFold(ans, "yes") && (len(newer) > 0 || !strings.EqualFold(ans, "y")) {
			fmt.Fprintln(stdout, "\nNothing was restored.")
			return 1
		}
	}
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
	if err := snapshot.MarkUndone(store, id); err != nil {
		fmt.Fprintln(stdout, "Note: could not mark this snapshot as undone:", err)
	}
	if len(m.LeftOut) > 0 {
		fmt.Fprintln(stdout, "Verified: every saved file matches what it was before. The items that could not be read were not saved, so they are not restored.")
		return 0
	}
	fmt.Fprintln(stdout, "Verified: every file matches what it was before.")
	return 0
}

// describeUndo says which snapshot is about to be restored and what that would overwrite.
func describeUndo(w io.Writer, m snapshot.Manifest, newer []string) {
	fmt.Fprintf(w, "Undo this command: %s\n", report.Printable(m.Line))
	fmt.Fprintf(w, "  run:    %s\n", m.Created.Local().Format("2006-01-02 15:04"))
	if m.Dir != "" {
		fmt.Fprintf(w, "  folder: %s\n", report.Printable(m.Dir))
	}
	fmt.Fprintf(w, "  saved:  %s\n", report.Files(len(m.Entries)))
	if len(m.LeftOut) > 0 {
		fmt.Fprintf(w, "  not saved: %s\n", leftOutText(m.LeftOut))
	}
	if m.Undone != nil {
		fmt.Fprintf(w, "This was already undone on %s.\n", m.Undone.Local().Format("2006-01-02 15:04"))
	}
	if len(newer) > 0 {
		fmt.Fprintf(w, "WARNING: %s changed since then. Restoring will overwrite that newer work:\n", report.Files(len(newer)))
		for i, p := range newer {
			if i == 5 {
				fmt.Fprintf(w, "  - and %d more\n", len(newer)-5)
				break
			}
			fmt.Fprintf(w, "  - %s\n", report.Printable(p))
		}
	}
}

// leftOutText says which paths a snapshot could not save, and that undo cannot bring them back.
func leftOutText(l []snapshot.LeftOut) string {
	what := "1 item"
	if len(l) > 1 {
		what = fmt.Sprintf("%d items", len(l))
	}
	var parts []string
	for i, o := range l {
		if i == 3 {
			parts = append(parts, fmt.Sprintf("and %d more", len(l)-3))
			break
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", report.Printable(o.Path), o.Reason))
	}
	return what + " that could not be read, so undo cannot bring them back: " + strings.Join(parts, ", ")
}
