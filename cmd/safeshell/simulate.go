package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// runSimulate is `safeshell simulate`: run a command in the sandbox and show what it changed.
func runSimulate(args []string, stdout, stderr io.Writer) int {
	f, args, ok := parseFlags("simulate", args, true, stderr)
	if !ok {
		return 2
	}
	spec := sandbox.Spec{Cwd: f.cwd, Timeout: f.timeout}
	spec.Line = strings.Join(args, " ")
	ctx, stop := interruptible()
	defer stop()
	res, err := simulate(ctx, spec)
	if ctx.Err() != nil {
		fmt.Fprintln(stderr, interruptedMsg)
		return exitInterrupted
	}
	if err != nil {
		fmt.Fprintf(stderr, "safeshell simulate: %s\n", report.Printable(err.Error()))
		return 1
	}
	if f.json {
		printJSON(stdout, res)
	} else {
		simReport(stdout, spec.Line, cwdOr(spec.Cwd), res, f.verbose)
	}
	return 0
}

const maxListed = 15

func simReport(w io.Writer, line, cwd string, r sandbox.Result, verbose bool) {
	status := fmt.Sprintf("exit %d", r.ExitCode)
	if r.TimedOut {
		status = "timed out"
	}
	t := r.Diff.Totals()
	fmt.Fprintf(w, "Simulated in %s (%s). Your real files were not touched.\n$ %s\n\n", r.Duration.Round(time.Millisecond), status, report.Printable(line))
	fmt.Fprintf(w, "  created   %s\n  modified  %s\n  deleted   %s", report.Files(t.Created), report.Files(t.Modified), report.Files(t.Deleted))
	if t.Deleted > 0 {
		fmt.Fprintf(w, " (%s)", report.HumanBytes(t.DeletedBytes))
	}
	if r.Diff.Truncated {
		fmt.Fprint(w, " (count stopped at a limit)")
	}
	fmt.Fprintln(w)
	if len(r.Diff.Changes) > 0 {
		fmt.Fprintln(w, "\nChanges")
		for i, c := range r.Diff.Changes {
			if i == maxListed {
				fmt.Fprintf(w, "  ... and %d more\n", len(r.Diff.Changes)-maxListed)
				break
			}
			dir := ""
			if c.IsDir {
				dir = "/"
			}
			extra := ""
			if c.IsDir && c.Files > 0 {
				extra = fmt.Sprintf("  (%s)", report.Files(c.Files))
			}
			fmt.Fprintf(w, "  %-9s %s%s%s\n", c.Kind, report.Printable(c.Abs()), dir, extra)
		}
	}
	shown, hidden := sandboxNotes(r.Warnings, line, cwd, r.Diff.Changes, verbose)
	for _, wn := range shown {
		fmt.Fprintf(w, "\nWarning: %s\n", report.Printable(wn.Text))
	}
	if hidden > 0 {
		fmt.Fprintln(w)
		hiddenNotes(w, hidden, "", "")
	}
	for _, o := range []struct{ name, text string }{{"stdout", r.Stdout}, {"stderr", r.Stderr}} {
		if strings.TrimSpace(o.text) != "" {
			fmt.Fprintf(w, "\n%s\n%s", o.name, indent(o.text))
		}
	}
	if r.Truncated {
		fmt.Fprintln(w, "  (output cut)")
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + report.Printable(l)
	}
	return strings.Join(lines, "\n") + "\n"
}
