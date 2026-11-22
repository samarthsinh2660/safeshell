package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/policy"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/shellhook"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
	"github.com/samarthsinh2660/safeshell/internal/ui"
)

// exitIncomplete is what guard returns for a command the shell is still reading.
const exitIncomplete = 3

// guardInput is where answers are read from; tests replace it.
var guardInput = func() io.Reader {
	if t, err := os.Open("/dev/tty"); err == nil {
		return t
	}
	return os.Stdin
}

// runGuard is what the shell hook calls for every command line. The command to
// run goes to stdout (the original, or a safer option the person picked); the
// analysis and questions go to stderr. Exit 0 runs it, 1 cancels. With
// --multiline (sent by hooks that collect the lines of an unfinished command),
// exitIncomplete means the command is not finished (an open here-document, quote
// or trailing backslash): nothing was checked, and the hook asks again once the
// command is complete. Without it, as from a hook printed by an older SafeShell,
// every line is checked as it comes, so an open shell is never less protected.
func runGuard(args []string, stdout, stderr io.Writer, color bool) int {
	cwd, unknown, multiline, agent := "", false, false, false
opts:
	for len(args) > 0 {
		switch {
		case args[0] == "--":
			args = args[1:]
			break opts
		case args[0] == "--unknown":
			unknown, args = true, args[1:]
		case args[0] == "--multiline":
			multiline, args = true, args[1:]
		case args[0] == "--agent":
			agent, args = true, args[1:]
		case args[0] == "--cwd" && len(args) >= 2:
			cwd, args = args[1], args[2:]
		case strings.HasPrefix(args[0], "-"):
			unknownOption("guard", args, stderr)
			return 2
		default:
			break opts
		}
	}
	if agent && len(args) == 0 && cwd == "" && !unknown && !multiline {
		return runGuardAgent(stdout, stderr)
	}
	if agent || len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	tourHint(stderr)
	cwd = cwdOr(cwd)
	line := strings.Join(args, " ")
	if code, ok := slashCommand(line, stderr); ok {
		return code
	}
	// A line the shell would reject as "command not found", or one starting with ?, may be English.
	if text, ok := nl.LooksLikeRequest(line, !unknown); ok && !strings.Contains(line, "\n") {
		code, available := askLine(text, cwd, stdout, stderr, color)
		if available || strings.HasPrefix(strings.TrimSpace(line), "?") {
			return code
		}
		// Not English after all, or no AI: let the shell handle the line as typed.
		fmt.Fprintln(stdout, line)
		return 0
	}
	if multiline && shellparse.Incomplete(line) {
		return exitIncomplete
	}
	return guardLine(line, cwd, stdout, stderr, color, fromPerson)
}

// changesShell is true for a line that sets up the shell itself, or that cannot be read.
func changesShell(line string) bool {
	r, err := shellparse.Parse(line)
	return err != nil || r.ChangesShell
}

// origin says who wrote the line guardLine is checking.
type origin int

const (
	fromPerson    origin = iota // the person typed it
	fromAssistant               // the AI wrote it from a plain-English request
	fromUndo                    // "safeshell undo" proposed it and the person already agreed to run it
	fromTask                    // "safeshell do" wrote it after reading files, so autorun never applies
)

// guardLine analyses one command line, asks if needed, and prints what to run.
func guardLine(line, cwd string, stdout, stderr io.Writer, color bool, from origin) int {
	_ = snapshot.Seal(snapshotDir(), os.Getenv("SAFESHELL_HOOK")) // the previous command here has finished: note what it left, so undo can spot later edits
	ctx, stop := interruptible()
	a := analyzeFast(ctx, line, cwd, stderr)
	interrupted := ctx.Err() != nil
	stop() // the questions below read the terminal, where Ctrl-C must keep its usual effect
	if interrupted {
		fmt.Fprintln(stderr, cancelledMsg)
		return 1 // nothing goes to stdout, so the hook runs nothing
	}
	trusted, trustOn := trustSet()
	// A line trusted here runs without a question, so it needs no AI-proposed alternative;
	// the review still runs and can still raise it.
	aiReview(&a, cwd, stderr, !a.TrustedHere(trusted, cwd))
	a.ApplyTrust(trusted, cwd) // last: the AI can still raise it, and nothing but plain R2 is ever let through
	printPolicyWarning(stderr, a)
	printHostNote(stderr, a)
	if a.Decision != domain.Allow {
		printAnalysis(stderr, a, cwd, color, true)
	} else {
		if a.Trusted {
			fmt.Fprintln(stderr, "You told SafeShell to always run this command in this folder, so it was not asked about. See or undo that with: safeshell trust list")
		}
		learnNote(stderr, a, color)
	}
	if a.Team.Block != "" { // the admin's block is final: no "do it anyway" here
		record(a, line, false)
		fmt.Fprintln(stderr, "Ask your administrator if you need this to run.")
		return 1
	}
	in := bufio.NewReader(guardInput())
	cmd, ok := line, true
	if from == fromAssistant && runsUnasked(line, cwd, a) {
		a.Autorun = true
		fmt.Fprintln(stderr, "Running it now. Turn this off with: safeshell autorun off")
	} else if from != fromPerson && a.Decision == domain.Allow {
		proceed := ui.Proceed
		if plainOn() {
			proceed = ui.ProceedPlain
		}
		// The assistant wrote it, so the person always sees it first. In a task Enter means no: the model has read
		// files, and a stray Enter typed ahead must never approve a step.
		ok = proceed(in, stderr, from != fromTask && !changesShell(line))
	} else {
		choose := ui.Choose
		if plainOn() {
			choose = ui.ChoosePlain
		}
		var always bool
		cmd, ok, always = choose(in, stderr, line, a.Decision, a.Verdict.Alternatives, trustOn && from != fromTask && a.CanTrust(cwd))
		if always && ok {
			saveTrust(policy.NewTrust(line, cwd), stderr)
		}
	}
	record(a, cmd, ok)
	if ok && cmd == line { // a replacement command has different effects, so the saved files do not apply
		note := protect(a, cwd)
		if note == "" && a.Team.Snapshot {
			note = teamNoSnapshot
		}
		if note != "" {
			fmt.Fprintln(stderr, report.Printable(note))
		}
	}
	if !ok {
		fmt.Fprintln(stderr, cancelledMsg)
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
	bin, err := executablePath()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell hook: cannot find where safeshell is installed:", err)
		return 1
	}
	script := shellhook.Script
	if strictOn(stderr) {
		script = shellhook.StrictScript
	}
	s, err := script(args[0], bin)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell hook:", err)
		return 2
	}
	fmt.Fprint(stdout, s)
	return 0
}

// auditPath is where decisions are logged; tests replace it.
var auditPath = audit.DefaultPath

// record logs a decision. Lines starting with a space are kept out, like shell
// history with HISTCONTROL=ignorespace, and secret values are masked.
func record(a engine.Analysis, cmd string, ran bool) {
	if strings.HasPrefix(a.Line, " ") {
		return
	}
	outcome, run := audit.Outcome(a.Decision, ran, a.Line, cmd)
	if a.Trusted && outcome == "allowed" {
		outcome = audit.OutcomeTrusted
	}
	if a.Autorun && outcome == "allowed" {
		outcome = audit.OutcomeAutorun
	}
	e := audit.Entry{Time: time.Now().UTC(), Line: report.MaskSecrets(a.Line), Run: report.MaskSecrets(run), Level: a.Level.String(), Decision: a.Decision.String(), Outcome: outcome}
	if a.AI != nil {
		e.AIProvider, e.AIRaised, e.AILevel, e.AIReason = a.AI.Provider, a.AI.Raised, a.AI.Level.String(), report.MaskSecrets(a.AI.Reason)
	}
	logEntry(e)
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
		runModels(nil, guardInput(), w, w)
	case "/help":
		runHelp(f[1:], w, w)
	default:
		return 0, false
	}
	return 1, true
}
