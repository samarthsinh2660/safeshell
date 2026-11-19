package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// runCheck is `safeshell check`: rate a command without running it.
func runCheck(args []string, stdout, stderr io.Writer, color bool) int {
	if len(args) > 0 && args[0] == "--script" {
		return runCheckScript(args[1:], stdout, stderr, color)
	}
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
	v := classify.Classify(line, userHome())
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

// userHome is the home folder the rules protect; empty when it is unknown.
func userHome() string {
	h, _ := os.UserHomeDir()
	return h
}

// readScript reads the script a command was pointed at and rates it line by
// line. It prints the problem itself and returns ok false when it cannot.
func readScript(cmd string, args []string, stderr io.Writer) (src string, lines []classify.LineFinding, ok bool) {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return "", nil, false
	}
	b, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "safeshell %s: cannot read %s: %v\nCheck the path and that you may read the file.\n", cmd, report.Printable(args[0]), err)
		return "", nil, false
	}
	lines, err = classify.Script(string(b), userHome())
	if err != nil {
		fmt.Fprintf(stderr, "safeshell %s: %s is not valid shell, %s\nFix that line, then check again.\n", cmd, report.Printable(args[0]), report.Printable(err.Error()))
		return "", nil, false
	}
	return string(b), lines, true
}

// runCheckScript is `safeshell check --script FILE`: rate every command in a
// script, say on which line it is, and give the highest level as the result.
func runCheckScript(args []string, stdout, stderr io.Writer, color bool) int {
	_, lines, ok := readScript("check", args, stderr)
	if !ok {
		return 2
	}
	level := classify.ScriptLevel(lines)
	c := newPalette(color)
	printScriptLines(stdout, lines, c)
	fmt.Fprintf(stdout, "\n%s%s %s%s  %s\n", c.bold+c.level(level), level, level.Label(), c.reset, levelAction[level])
	return level.ExitCode()
}

// printScriptLines prints one row for each line rated above R0, in line order.
func printScriptLines(w io.Writer, lines []classify.LineFinding, c palette) {
	shown := false
	for _, lf := range lines {
		if lf.Level == domain.R0 {
			continue
		}
		shown = true
		for i, reason := range lf.Reasons {
			num := ""
			if i == 0 {
				num = fmt.Sprint(lf.Line)
			}
			fmt.Fprintf(w, "%5s  %s%s%s  %s\n", num, c.level(lf.Level), lf.Level, c.reset, report.Printable(reason))
		}
		fmt.Fprintf(w, "       %s%s%s\n", c.dim, report.Printable(lf.Text), c.reset)
	}
	if !shown {
		fmt.Fprintln(w, "No risky commands found.")
	}
}
