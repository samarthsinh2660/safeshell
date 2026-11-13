// Command safeshell is the SafeShell command-line tool.
//
// Phase 1 provides `safeshell check`, which rates a command without running it.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/overlayfs"
)

// probe and simulate are replaced in tests.
var (
	probe    = platform.Probe
	simulate = overlayfs.Run
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const usage = `SafeShell: a safety layer for your terminal.

Usage:
  safeshell check [--json] <command...>     rate a command without running it
  safeshell analyze [--json] [--verbose] [--cwd DIR] <command...>
                                            the full picture: risk level, a sandbox run, the real
                                            processes and disks it would hit, and safer options
  safeshell simulate [--json] [--verbose] [--cwd DIR] [--timeout 30s] <command...>
                                            run a command in a sandbox and show what it changed;
                                            the real files are never touched
  safeshell guard [--cwd DIR] -- <command...>
                                            what the shell hook runs: analyse, ask, print the command to run
  safeshell explain <command...>            the analysis plus a plain-English explanation (needs AI)
  safeshell ask <what you want in English>
                                            AI proposes a command; it is checked like any other
  safeshell models [list]                   choose the AI model (menu), or list the models your cloud key offers
  safeshell ai setup|status                 download the built-in model (once) / show AI state
  safeshell voice [setup|status|--text]     speak a request (local speech engine; set up once with  voice setup)
  safeshell undo [ID]                       put back what the last approved command changed, and verify it
  safeshell snapshots                       list saved snapshots
  safeshell daemon [stop]                   keep SafeShell warm in the background (optional; faster checks)
  safeshell install [--ai]                  add the hook to your shell startup file (and set up the AI)
  safeshell uninstall                       remove the hook
  safeshell hook bash|zsh                   print the shell hook; add  eval "$(safeshell hook bash)"  to your rc file
  safeshell version                         which release this is
  safeshell doctor                          show what this machine allows (sandbox, snapshots, AI)

Quote the command so your shell passes it through untouched:
  safeshell check 'rm -rf ~/projects'

Exit status of 'safeshell check': 0 = R0-R2, 3 = R3 (irreversible), 4 = R4 (catastrophic).
`

func main() {
	// The sandbox re-executes this binary; that path must run before anything else.
	if len(os.Args) > 1 && os.Args[1] == "sandbox-init" {
		os.Exit(overlayfs.Init())
	}
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, useColor(os.Stdout)))
}

// run is main without the process-level parts, so tests can drive it.
func run(args []string, stdout, stderr io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if args[0] == "-h" || args[0] == "--help" {
		return runHelp(nil, stdout)
	}
	switch args[0] {
	case "check":
		return check(args[1:], stdout, stderr, color)
	case "analyze":
		return runAnalyze(args[1:], stdout, stderr, color)
	case "simulate":
		return runSimulate(args[1:], stdout, stderr)
	case "explain":
		return runExplain(args[1:], stdout, stderr, color)
	case "daemon":
		return runDaemon(args[1:], stdout, stderr)
	case "install":
		return runInstall(args[1:], stdout, stderr)
	case "uninstall":
		return runUninstall(stdout, stderr)
	case "models":
		if len(args) == 2 && args[1] == "list" {
			return runModelsList(context.Background(), stdout, stderr, listCloud)
		}
		return runModels(guardInput(), stdout)
	case "help":
		return runHelp(args[1:], stdout)
	case "ai":
		return runAI(args[1:], stdout, stderr)
	case "voice":
		return runVoice(args[1:], stdout, stderr, color)
	case "ask":
		return runAsk(args[1:], stdout, stderr, color)
	case "undo":
		return runUndo(args[1:], stdout, stderr)
	case "snapshots":
		return runSnapshots(stdout, stderr)
	case "guard":
		return runGuard(args[1:], stdout, stderr, color)
	case "hook":
		return runHook(args[1:], stdout, stderr)
	case "version", "--version":
		fmt.Fprintln(stdout, "safeshell", version)
		return 0
	case "doctor":
		code := doctor(stdout, probe())
		doctorAI(stdout)
		cards, _ := os.ReadFile("/proc/asound/cards")
		doctorVoice(stdout, voiceSetup(), realRunner, string(cards))
		return code
	default:
		fmt.Fprintf(stderr, "safeshell: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

func check(args []string, stdout, stderr io.Writer, color bool) int {
	asJSON := false
	if len(args) > 0 && args[0] == "--json" {
		asJSON, args = true, args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	line := strings.Join(args, " ")
	v := classify.Classify(line)
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	} else {
		printCheck(stdout, line, v, color)
	}
	switch v.Level {
	case domain.R4:
		return 4
	case domain.R3:
		return 3
	}
	return 0
}

var decision = [...]string{"ALLOW", "ALLOW (undo recorded)", "SIMULATE FIRST, THEN SNAPSHOT", "BLOCK (you can override)", "HARD BLOCK"}

func printCheck(w io.Writer, line string, v domain.Verdict, color bool) {
	c := newPalette(color)
	col := [...]string{c.green, c.green, c.yellow, c.red, c.red}[v.Level]
	fmt.Fprintf(w, "%s%s %s%s  %s\n", c.bold+col, v.Level, v.Level.Label(), c.reset, decision[v.Level])
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
	if len(v.Alternatives) > 0 {
		fmt.Fprintf(w, "\n%sSafer options%s\n", c.bold, c.reset)
		for i, a := range v.Alternatives {
			fmt.Fprintf(w, "  %d) %s%s%s\n     %s\n", i+1, c.green, report.Printable(a.Command), c.reset, report.Printable(a.Note))
		}
	}
	if len(v.Unclassified) > 0 {
		fmt.Fprintf(w, "\n%sNot recognised yet: %s (rated R1; later phases test these in a sandbox)%s\n", c.dim, report.Printable(strings.Join(v.Unclassified, ", ")), c.reset)
	}
}

type palette struct{ bold, dim, red, green, yellow, reset string }

func newPalette(on bool) palette {
	if !on {
		return palette{}
	}
	return palette{"\033[1m", "\033[2m", "\033[31m", "\033[32m", "\033[33m", "\033[0m"}
}

// useColor is true only when f is a terminal and NO_COLOR is unset.
func useColor(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
}

func doctor(w io.Writer, r platform.Report) int {
	row := func(name string, ok bool, detail string) {
		mark := "ok     "
		if !ok {
			mark = "MISSING"
		}
		fmt.Fprintf(w, "  %s  %-26s %s\n", mark, name, detail)
	}
	fmt.Fprintf(w, "SafeShell doctor (kernel %s)\n\n", r.Kernel)
	row("user namespaces", r.UserNS, r.UserNSErr)
	row("overlay filesystem", r.Overlay, "")
	row("seccomp", r.Seccomp, "")
	row("landlock", r.Landlock, "")
	row("cgroup v2", r.CgroupV2, "")
	fmt.Fprintf(w, "  info     %-26s %s\n", "snapshot backend", r.SnapshotBackend()+" (root is "+r.RootFS+")")
	fmt.Fprintf(w, "  info     %-26s %s\n", "shells found", strings.Join(r.Shells, ", "))
	fmt.Fprintf(w, "\nSandbox simulation: %v\n", map[bool]string{true: "available", false: "NOT available"}[r.CanSimulate()])
	for _, h := range r.Hints() {
		fmt.Fprintf(w, "  - %s\n", h)
	}
	if r.CanSimulate() {
		return 0
	}
	return 1
}

func runSimulate(args []string, stdout, stderr io.Writer) int {
	spec := sandbox.Spec{}
	asJSON, verbose := false, false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--json":
			asJSON = true
		case "--verbose":
			verbose = true
		case "--cwd", "--timeout":
			if len(args) < 2 {
				fmt.Fprint(stderr, usage)
				return 2
			}
			if args[0] == "--cwd" {
				spec.Cwd = args[1]
			} else if d, err := time.ParseDuration(args[1]); err != nil {
				fmt.Fprintf(stderr, "safeshell simulate: bad --timeout %q (try 5s)\n", args[1])
				return 2
			} else {
				spec.Timeout = d
			}
			args = args[1:]
		default:
			fmt.Fprintf(stderr, "safeshell simulate: unknown option %s\n", args[0])
			return 2
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	spec.Line = strings.Join(args, " ")
	res, err := simulate(context.Background(), spec)
	if err != nil {
		fmt.Fprintf(stderr, "safeshell simulate: %s\n", report.Printable(err.Error()))
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		cwd := spec.Cwd
		if cwd == "" {
			cwd, _ = os.Getwd()
		}
		simReport(stdout, spec.Line, cwd, res, verbose)
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
			fmt.Fprintf(w, "  %-9s /%s%s%s\n", c.Kind, report.Printable(c.Path), dir, extra)
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
