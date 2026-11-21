package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
	"github.com/samarthsinh2660/safeshell/internal/undo"
	"github.com/samarthsinh2660/safeshell/internal/undo/host"
)

// aptHistoryPath is apt's transaction log; tests replace it.
var aptHistoryPath = host.AptHistoryPath

// unitRunner asks systemctl about a unit; tests replace it.
var unitRunner host.Runner = host.Exec

// runCommand runs an approved plan with the terminal attached, so sudo can ask
// for the password; tests replace it.
var runCommand = func(cmd, cwd string, stdout, stderr io.Writer) error {
	c := exec.Command("sh", "-c", cmd)
	c.Dir, c.Stdin, c.Stdout, c.Stderr = cwd, os.Stdin, stdout, stderr
	return c.Run()
}

// servicePlans reads, before a systemctl stop, disable or mask runs, how each
// unit is now (read-only) and returns the commands that put it back.
func servicePlans(line string) []undo.Plan {
	c, ok := undo.ParseServiceCmd(line)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var plans []undo.Plan
	for _, u := range c.Units {
		if st, ok := host.UnitState(ctx, unitRunner, u); ok {
			if p, ok := c.Restore(u, st); ok {
				plans = append(plans, p)
			}
		}
	}
	return plans
}

const aptNotFound = "No packages to remove were found in apt's log for this command: it may not have run, or it installed nothing new."

// planFor lists the inverse plans of a snapshot: the ones recorded before the
// command ran, and for an apt install, the one read now from apt's log. note
// says why an apt install has none.
func planFor(m snapshot.Manifest) (plans []undo.Plan, note string) {
	plans = m.Plans
	if !undo.AptInstall(m.Line) {
		return plans, ""
	}
	txs, err := host.AptHistory(aptHistoryPath)
	if err != nil {
		return plans, "SafeShell could not read apt's log (" + aptHistoryPath + "), so it cannot say which packages to remove."
	}
	p, ok := undo.AptRemovePlan(txs, m.Line, m.Created)
	if !ok {
		return plans, aptNotFound
	}
	return append(plans, p), ""
}

// describePlans shows what else undo can reverse, and that it is never done automatically.
func describePlans(w io.Writer, plans []undo.Plan, note string) {
	if note != "" {
		fmt.Fprintln(w, note)
	}
	for _, p := range plans {
		fmt.Fprintf(w, "To reverse the system change:\n  %s\n  %s\n", report.Printable(p.Command), report.Printable(p.Explain))
	}
}

// runPlans offers each plan and, when the person says yes, runs it the way a
// typed command runs: through the guard, which analyses and asks. It reports
// whether every plan ran.
func runPlans(plans []undo.Plan, cwd string, in io.Reader, stdout, stderr io.Writer, color bool) bool {
	fmt.Fprint(stdout, "Run it now? SafeShell checks it first, like any command you type. Type yes to continue: ")
	if ans, _ := readLine(in); !strings.EqualFold(ans, "yes") {
		fmt.Fprintln(stdout, "\nNothing was run.")
		return false
	}
	for _, p := range plans {
		var cmd strings.Builder
		if guardLine(p.Command, cwd, &cmd, stderr, color, true) != 0 {
			return false
		}
		if err := runCommand(strings.TrimSpace(cmd.String()), cwd, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, "safeshell undo: that command failed:", err)
			return false
		}
	}
	return true
}
