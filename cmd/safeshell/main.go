// Command safeshell is the SafeShell command-line tool and the composition root
// that wires the adapters (sandbox, snapshots, AI, daemon) into the engine.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/overlayfs"
)

// probe and simulate are replaced in tests.
var (
	probe    = platform.Probe
	simulate = overlayfs.Run
)

// version is stamped at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

// Messages shown in more than one place.
const (
	cancelledMsg = "Cancelled. Nothing was run."
	aiOffHint    = "Type /models to choose a model, or run  safeshell ai setup"
)

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
  safeshell voice [setup [--yes]|status|--text]
                                            speak a request (local speech engine; set up once with  voice setup)
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
		return runCheck(args[1:], stdout, stderr, color)
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
		return runModels(args[1:], guardInput(), stdout, stderr)
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
		return runDoctor(stdout)
	default:
		fmt.Fprintf(stderr, "safeshell: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}

type palette struct{ bold, dim, red, green, yellow, reset string }

func newPalette(on bool) palette {
	if !on {
		return palette{}
	}
	return palette{"\033[1m", "\033[2m", "\033[31m", "\033[32m", "\033[33m", "\033[0m"}
}

// level is the colour for a risk level: green R0-R1, yellow R2, red R3-R4.
func (c palette) level(l domain.Level) string {
	return [...]string{c.green, c.green, c.yellow, c.red, c.red}[l]
}

// useColor is true only when f is a terminal and NO_COLOR is unset.
func useColor(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0 && os.Getenv("NO_COLOR") == ""
}

// printJSON writes v as indented JSON.
func printJSON(w io.Writer, v any) {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// printAlternatives lists the safer commands, if there are any.
func printAlternatives(w io.Writer, c palette, alts []domain.Alternative) {
	if len(alts) == 0 {
		return
	}
	fmt.Fprintf(w, "\n%sSafer options%s\n", c.bold, c.reset)
	for i, a := range alts {
		fmt.Fprintf(w, "  %d) %s%s%s\n     %s\n", i+1, c.green, report.Printable(a.Command), c.reset, report.Printable(a.Note))
	}
}

// cwdOr is dir, or the current directory when dir is empty.
func cwdOr(dir string) string {
	if dir != "" {
		return dir
	}
	d, _ := os.Getwd()
	return d
}
