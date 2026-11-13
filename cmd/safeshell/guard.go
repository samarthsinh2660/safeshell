package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/shellhook"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
	"github.com/samarthsinh2660/safeshell/internal/ui"
)

// guardInput is where answers are read from; tests replace it.
var guardInput = func() io.Reader {
	if t, err := os.Open("/dev/tty"); err == nil {
		return t
	}
	return os.Stdin
}

// runGuard is what the shell hook calls for every command line. The command to
// run goes to stdout (the original, or a safer option the person picked); the
// analysis and questions go to stderr. Exit 0 runs it, 1 cancels.
func runGuard(args []string, stdout, stderr io.Writer, color bool) int {
	cwd, unknown := "", false
opts:
	for len(args) > 0 {
		switch {
		case args[0] == "--":
			args = args[1:]
			break opts
		case args[0] == "--unknown":
			unknown, args = true, args[1:]
		case args[0] == "--cwd" && len(args) >= 2:
			cwd, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "-"):
			fmt.Fprintf(stderr, "safeshell guard: bad option %s\n", args[0])
			return 2
		default:
			break opts
		}
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	line := strings.Join(args, " ")
	if code, ok := slashCommand(line, stderr); ok {
		return code
	}
	// A line the shell would reject as "command not found", or one starting with ?, may be English.
	if text, ok := nl.LooksLikeRequest(line, !unknown); ok {
		code, available := askLine(text, cwd, stdout, stderr, color)
		if available || strings.HasPrefix(strings.TrimSpace(line), "?") {
			return code
		}
		// Not English after all, or no AI: let the shell handle the line as typed.
		fmt.Fprintln(stdout, line)
		return 0
	}
	return guardLine(line, cwd, stdout, stderr, color, false)
}

// changesShell is true for a line that sets up the shell itself, or that cannot be read.
func changesShell(line string) bool {
	r, err := shellparse.Parse(line)
	return err != nil || r.ChangesShell
}

// guardLine analyses one command line, asks if needed, and prints what to run.
func guardLine(line, cwd string, stdout, stderr io.Writer, color bool, proposed bool) int {
	a := analyzeFast(context.Background(), line, cwd, stderr)
	aiReview(&a, stderr)
	if a.Decision != domain.Allow {
		printAnalysis(stderr, a, cwd, color, true)
	}
	in := bufio.NewReader(guardInput())
	cmd, ok := line, true
	if proposed && a.Decision == domain.Allow {
		ok = ui.Proceed(in, stderr, !changesShell(line)) // the assistant wrote it, so the person always sees it first
	} else {
		cmd, ok = ui.Choose(in, stderr, line, a.Decision, a.Verdict.Alternatives)
	}
	record(a, cmd, ok)
	if ok && cmd == line { // a replacement command has different effects, so the saved files do not apply
		if note := protect(a); note != "" {
			fmt.Fprintln(stderr, report.Printable(note))
		}
	}
	if !ok {
		fmt.Fprintln(stderr, "Cancelled. Nothing was run.")
		return 1
	}
	fmt.Fprintln(stdout, cmd)
	return 0
}

func runHook(args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	s, err := shellhook.Script(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "safeshell hook:", err)
		return 2
	}
	fmt.Fprint(stdout, s)
	return 0
}

// auditPath is where decisions are logged; tests replace it.
var auditPath = audit.DefaultPath

func record(a engine.Analysis, cmd string, ran bool) {
	e := audit.Entry{Time: time.Now().UTC(), Line: a.Line, Level: a.Level.String(), Decision: a.Decision.String(), Outcome: "cancelled"}
	switch {
	case ran && a.Decision == domain.Allow:
		e.Outcome = "allowed"
	case ran && cmd != a.Line:
		e.Outcome, e.Run = "replaced", cmd
	case ran:
		e.Outcome = "ran"
	}
	_ = audit.Append(auditPath(), e) // a full disk must not stop a command
}

// slashCommand handles lines like /models typed at the prompt. They open a
// menu or a guide and never run anything, so the exit code is always "cancel".
func slashCommand(line string, w io.Writer) (code int, ok bool) {
	f := strings.Fields(line)
	if len(f) == 0 || !strings.HasPrefix(f[0], "/") {
		return 0, false
	}
	switch f[0] {
	case "/models":
		runModels(guardInput(), w)
	case "/help":
		runHelp(f[1:], w)
	default:
		return 0, false
	}
	return 1, true
}
