package main

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// runCheck is `safeshell check`: rate a command without running it.
func runCheck(args []string, stdout, stderr io.Writer, color bool) int {
	if hasScriptOption(args) {
		return runCheckScript(args, stdout, stderr, color)
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
	if plainOn() {
		printPlainCheck(w, line, v)
		return
	}
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

// hasScriptOption is true when --script is among the leading options.
func hasScriptOption(args []string) bool {
	for i := 0; i < len(args) && strings.HasPrefix(args[i], "-"); i++ {
		switch args[i] {
		case "--script":
			return true
		case "--format", "--fail-on":
			i++ // skip the value
		}
	}
	return false
}

// scriptOptions are what `check --script` was asked for.
type scriptOptions struct {
	files  []string
	format string
	failOn domain.Level
}

// parseScriptOptions reads the files and options of `check --script`. ok is
// false once it has printed what was wrong.
func parseScriptOptions(args []string, stderr io.Writer) (o scriptOptions, ok bool) {
	o = scriptOptions{format: "text", failOn: domain.R3}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--script":
		case a == "--format" || a == "--fail-on":
			if i++; i == len(args) {
				fmt.Fprint(stderr, usage)
				return o, false
			}
			if a == "--format" {
				o.format = args[i]
				if !slices.Contains([]string{"text", "json", "sarif", "github"}, o.format) {
					fmt.Fprintf(stderr, "safeshell check: unknown format %s; use text, json, sarif or github\n", report.Printable(o.format))
					return o, false
				}
			} else if o.failOn, ok = domain.ParseLevel(args[i]); !ok || o.failOn < domain.R2 {
				fmt.Fprintf(stderr, "safeshell check: --fail-on takes R2, R3 or R4, not %s\n", report.Printable(args[i]))
				return o, false
			}
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(stderr, "safeshell check: unknown option %s; with --script only --format and --fail-on are accepted\n", report.Printable(a))
			return o, false
		default:
			o.files = append(o.files, a)
		}
	}
	if len(o.files) == 0 {
		fmt.Fprint(stderr, usage)
		return o, false
	}
	return o, true
}

// runCheckScript is `safeshell check --script FILE...`: rate every command in
// each script, say on which line it is, and give the highest level as the
// result. It uses the rules only: no sandbox, no AI and no daemon.
func runCheckScript(args []string, stdout, stderr io.Writer, color bool) int {
	o, ok := parseScriptOptions(args, stderr)
	if !ok {
		return 2
	}
	var files []report.ScriptFile
	level, failed := domain.R0, false
	for _, path := range o.files {
		_, lines, ok := readScript("check", []string{path}, stderr)
		if !ok {
			failed = true
			continue
		}
		files = append(files, report.ScriptFile{Path: path, Lines: lines})
		level = max(level, classify.ScriptLevel(lines))
	}
	switch o.format {
	case "json":
		fmt.Fprintf(stdout, "%s\n", report.ScriptJSON(files))
	case "sarif":
		fmt.Fprintf(stdout, "%s\n", report.ScriptSARIF(files, version))
	case "github":
		fmt.Fprint(stdout, report.ScriptGitHub(files))
	default:
		printScripts(stdout, files, level, newPalette(color))
	}
	switch {
	case level < o.failOn && failed:
		return 2
	case level < o.failOn:
		return 0
	case level.ExitCode() == 0: // R2 with --fail-on R2
		return 1
	}
	return level.ExitCode()
}

// printScripts prints the rated lines of each script, headed by its path when
// there are several, then the highest level.
func printScripts(w io.Writer, files []report.ScriptFile, level domain.Level, c palette) {
	for _, f := range files {
		if len(files) > 1 {
			fmt.Fprintf(w, "%s%s%s\n", c.bold, report.Printable(f.Path), c.reset)
		}
		printScriptLines(w, f.Lines, c)
	}
	if len(files) > 0 && plainOn() {
		fmt.Fprintln(w, plainLevel(level), plainLevelAction[level])
	} else if len(files) > 0 {
		fmt.Fprintf(w, "\n%s%s %s%s  %s\n", c.bold+c.level(level), level, level.Label(), c.reset, levelAction[level])
	}
}

// printScriptLines prints one row for each line rated above R0, in line order.
func printScriptLines(w io.Writer, lines []classify.LineFinding, c palette) {
	if plainOn() {
		printPlainScriptLines(w, lines)
		return
	}
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
