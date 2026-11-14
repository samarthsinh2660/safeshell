package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/effects"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/overlayfs"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// analyze is replaced in tests.
var analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
	e := &engine.Engine{Sim: realSandbox{}, Inspectors: []engine.Inspector{inspectProcesses, inspectDisks, inspectPackages, inspectServices}}
	return e.Analyze(ctx, line, cwd)
}

// realSandbox runs the OverlayFS sandbox, after checking the machine allows it.
type realSandbox struct{}

func (realSandbox) Simulate(ctx context.Context, spec sandbox.Spec) (sandbox.Result, error) {
	if r := platform.Probe(); !r.CanSimulate() {
		return sandbox.Result{}, fmt.Errorf("sandbox unavailable: %s", strings.Join(r.Hints(), " "))
	}
	return overlayfs.Run(ctx, spec)
}

func inspectProcesses(cmds []shellparse.Command) []domain.Effect {
	return effects.InspectProcesses(effects.ProcFS{Root: "/proc"}, os.Getpid(), os.Getuid(), cmds)
}

func inspectDisks(cmds []shellparse.Command) []domain.Effect {
	mi, _ := os.ReadFile("/proc/self/mountinfo")
	return effects.InspectDisks(effects.SysFS{Dev: "/dev", Sys: "/sys", Mountinfo: string(mi)}, cmds)
}

// realRunner runs a read-only program with a short timeout and a plain locale.
func realRunner(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.Output()
	return string(out), err
}

func inspectPackages(cmds []shellparse.Command) []domain.Effect {
	return effects.PreviewPackages(realRunner, cmds)
}

func inspectServices(cmds []shellparse.Command) []domain.Effect {
	return effects.InspectServices(realRunner, cmds)
}

// flags are the options analyze and simulate share.
type flags struct {
	json, verbose bool
	cwd           string
	timeout       time.Duration // only read when parseFlags was asked for it
}

// parseFlags reads the leading --json, --verbose and --cwd DIR options (and
// --timeout DURATION when withTimeout is set) and returns the command words
// that follow. cmd names the subcommand in messages. ok is false once a message
// has been printed and the caller should exit 2.
func parseFlags(cmd string, args []string, withTimeout bool, stderr io.Writer) (f flags, rest []string, ok bool) {
	for len(args) > 0 && strings.HasPrefix(args[0], "--") && args[0] != "--" {
		switch {
		case args[0] == "--json":
			f.json = true
		case args[0] == "--verbose":
			f.verbose = true
		case args[0] == "--cwd" || (withTimeout && args[0] == "--timeout"):
			if len(args) < 2 {
				fmt.Fprint(stderr, usage)
				return f, nil, false
			}
			if args[0] == "--cwd" {
				f.cwd = args[1]
			} else if d, err := time.ParseDuration(args[1]); err != nil {
				fmt.Fprintf(stderr, "safeshell %s: bad --timeout %q (try 5s)\n", cmd, args[1])
				return f, nil, false
			} else {
				f.timeout = d
			}
			args = args[1:]
		default:
			unknownOption(cmd, args, stderr)
			return f, nil, false
		}
		args = args[1:]
	}
	args, _ = endOptions(cmd, args, stderr) // what is left is "--" or the command, never an option
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return f, nil, false
	}
	return f, args, true
}

func runAnalyze(args []string, stdout, stderr io.Writer, color bool) int {
	f, args, ok := parseFlags("analyze", args, false, stderr)
	if !ok {
		return 2
	}
	line := strings.Join(args, " ")
	cwd := cwdOr(f.cwd)
	a := analyze(context.Background(), line, cwd)
	if f.json {
		printJSON(stdout, a)
	} else {
		printAnalysis(stdout, a, cwd, color, f.verbose)
	}
	return a.Decision.ExitCode()
}

var decisionText = map[domain.Decision]string{
	domain.Allow:     "ALLOW",
	domain.Confirm:   "CONFIRM (look at the impact first)",
	domain.Block:     "BLOCK (you can override)",
	domain.HardBlock: "HARD BLOCK",
}

// sandboxNotes splits ws into the notes about places the command touches and
// a count of the rest. verbose, or any doubt about what the command touches,
// keeps them all.
func sandboxNotes(ws []sandbox.Warning, line, cwd string, changes []sandbox.Change, verbose bool) ([]sandbox.Warning, int) {
	if verbose {
		return ws, 0
	}
	home, _ := os.UserHomeDir()
	touched, uncertain := report.Touched(line, cwd, home, changes)
	if uncertain {
		return ws, 0
	}
	for _, p := range touched {
		if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
			touched = append(touched, real)
		}
	}
	shown := report.RelevantWarnings(ws, touched)
	return shown, len(ws) - len(shown)
}

func hiddenNotes(w io.Writer, n int, dim, reset string) {
	switch {
	case n == 1:
		fmt.Fprintf(w, "%s1 sandbox note about other folders was hidden; add --verbose to see it.%s\n", dim, reset)
	case n > 1:
		fmt.Fprintf(w, "%s%d sandbox notes about other folders were hidden; add --verbose to see them.%s\n", dim, n, reset)
	}
}

func printAnalysis(w io.Writer, a engine.Analysis, cwd string, color, verbose bool) {
	c := newPalette(color)
	col := c.level(a.Level)
	fmt.Fprintf(w, "%s%s %s%s  %s\n", c.bold+col, a.Level, a.Level.Label(), c.reset, decisionText[a.Decision])
	fmt.Fprintf(w, "%s$ %s%s\n", c.dim, report.Printable(a.Line), c.reset)
	if a.Level > a.Verdict.Level {
		fmt.Fprintf(w, "%sThe command text alone looked like %s; what it would really do raised it to %s.%s\n", c.dim, a.Verdict.Level, a.Level, c.reset)
	}

	shown := false
	for _, f := range a.Verdict.Findings {
		if f.Level == domain.R0 && a.Verdict.Level > domain.R0 {
			continue
		}
		if !shown {
			fmt.Fprintf(w, "\n%sWhy%s\n", c.bold, c.reset)
			shown = true
		}
		fmt.Fprintf(w, "  %s  %s\n", f.Level, report.Printable(f.Reason))
	}
	for _, e := range a.Effects {
		fmt.Fprintf(w, "\n%s%s%s  %s: %s\n", c.bold, e.Evidence, c.reset, e.Kind, report.Printable(e.Summary))
		for _, d := range e.Details {
			fmt.Fprintf(w, "      %s\n", report.Printable(d))
		}
	}
	if a.SimNote != "" {
		fmt.Fprintf(w, "\n%s%s%s\n", c.dim, report.Printable(a.SimNote), c.reset)
	}
	if a.Sim != nil {
		shown, hidden := sandboxNotes(a.Sim.Warnings, a.Line, cwd, a.Sim.Diff.Changes, verbose)
		for _, wn := range shown {
			fmt.Fprintf(w, "%sSandbox note: %s%s\n", c.dim, report.Printable(wn.Text), c.reset)
		}
		hiddenNotes(w, hidden, c.dim, c.reset)
	}
	printAlternatives(w, c, a.Verdict.Alternatives)
}
