package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// runCheck is `safeshell check`: rate a command without running it.
func runCheck(args []string, stdout, stderr io.Writer, color bool) int {
	asJSON := false
	if len(args) > 0 && args[0] == "--json" {
		asJSON, args = true, args[1:]
	}
	args, ok := endOptions("check", args, stderr)
	if !ok {
		return 2
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	line := strings.Join(args, " ")
	v := classify.Classify(line)
	if asJSON {
		printJSON(stdout, v)
	} else {
		printCheck(stdout, line, v, color)
	}
	return v.Level.ExitCode()
}

var levelAction = [...]string{"ALLOW", "ALLOW (undo recorded)", "SIMULATE FIRST, THEN SNAPSHOT", "BLOCK (you can override)", "HARD BLOCK"}

func printCheck(w io.Writer, line string, v domain.Verdict, color bool) {
	c := newPalette(color)
	col := c.level(v.Level)
	fmt.Fprintf(w, "%s%s %s%s  %s\n", c.bold+col, v.Level, v.Level.Label(), c.reset, levelAction[v.Level])
	fmt.Fprintf(w, "%s$ %s%s\n", c.dim, report.Printable(line), c.reset)
	for _, f := range v.Findings {
		if f.Level == domain.R0 && v.Level > domain.R0 {
			continue
		}
		fmt.Fprintf(w, "\n  %s%s%s  %s\n", c.bold, f.Level, c.reset, report.Printable(f.Reason))
		if f.Command != line {
			fmt.Fprintf(w, "      %s%s%s\n", c.dim, report.Printable(f.Command), c.reset)
		}
	}
	printAlternatives(w, c, v.Alternatives)
	if len(v.Unclassified) > 0 {
		fmt.Fprintf(w, "\n%sNot recognised yet: %s (rated R1; run  safeshell analyze  to test them in a sandbox)%s\n", c.dim, report.Printable(strings.Join(v.Unclassified, ", ")), c.reset)
	}
}
