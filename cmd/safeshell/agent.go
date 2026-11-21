package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/agenthook"
	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// agentInput is where `guard --agent` reads its JSON from; tests replace it.
var agentInput = func() io.Reader { return os.Stdin }

// agentBudget is the most time one agent check may take; a check that is not
// finished by then is denied. It is a variable so tests can shorten it.
var agentBudget = 30 * time.Second

// runGuardAgent answers one agent request: JSON in on stdin, JSON out on
// stdout, never a question and never the terminal. Anything that is not Allow
// is denied, because there is nobody to ask, and so is input it cannot read
// (the shell hook fails open, an agent hook fails closed). A Claude Code
// request is always answered with status 0, which is how Claude Code reads the
// JSON; any other request gets 0 for allow and 2 for deny.
func runGuardAgent(stdout, stderr io.Writer) int {
	req, err := agenthook.Parse(agentInput())
	if err != nil {
		ans := agenthook.Answer{Reason: "SafeShell did not run it: " + err.Error()}
		recordAgent("", domain.Block, ans)
		stdout.Write(agenthook.Format(false, ans))
		fmt.Fprintln(stderr, "safeshell guard --agent:", err)
		return 2
	}
	ans, d := agentAnswer(req)
	recordAgent(req.Command, d, ans)
	stdout.Write(agenthook.Format(req.Claude, ans))
	if ans.Allow || req.Claude {
		return 0
	}
	return 2
}

// recordAgent logs an agent decision. Unlike record it keeps every line,
// because an agent has no history to leave a line out of.
func recordAgent(line string, d domain.Decision, ans agenthook.Answer) {
	outcome := "agent-denied"
	if ans.Allow {
		outcome = "agent-allowed"
	}
	e := audit.Entry{Time: time.Now().UTC(), Line: report.MaskSecrets(line), Level: ans.Level, Decision: d.String(), Outcome: outcome}
	_ = audit.Append(auditPath(), e) // a full disk must not stop a command
}

// agentAnswer analyses the command like the hook does and turns the result into
// an answer. An analysis that was interrupted or ran out of time is a denial.
func agentAnswer(req agenthook.Request) (agenthook.Answer, domain.Decision) {
	ctx, cancel := context.WithTimeout(context.Background(), agentBudget)
	defer cancel()
	a := analyzeFast(ctx, req.Command, cwdOr(req.Cwd), io.Discard)
	if ctx.Err() != nil {
		return agenthook.Answer{Reason: "SafeShell did not run it: the check took longer than " + agentBudget.String() + ". Ask the person to run it."}, domain.Block
	}
	return answerFor(a), a.Decision
}

// answerFor says why a is allowed, or why it is denied and what to run instead.
func answerFor(a engine.Analysis) agenthook.Answer {
	ans := agenthook.Answer{Allow: a.Decision == domain.Allow, Level: a.Level.String()}
	if ans.Allow {
		ans.Reason = "SafeShell checked it: " + a.Level.String() + " " + a.Level.Label() + "."
		return ans
	}
	var why []string
	if a.Team.Block != "" {
		why = append(why, "blocked by your team's policy: "+a.Team.Block)
	}
	if a.Protected.Reason != "" {
		why = append(why, a.Protected.Reason)
	}
	for _, f := range a.Verdict.Findings {
		if f.Level > domain.R0 {
			why = append(why, f.Reason)
		}
	}
	for _, e := range a.Effects {
		why = append(why, e.Summary)
	}
	reason := "SafeShell did not run it: " + a.Level.String() + " " + a.Level.Label() + ", and nobody is there to confirm."
	if len(why) > 0 {
		reason += " Why: " + strings.Join(why, "; ") + "."
	}
	if alts := a.Verdict.Alternatives; len(alts) > 0 && a.Team.Block == "" {
		ans.Safer = alts[0].Command
		reason += " " + alts[0].Note + "."
	}
	ans.Reason = report.Printable(report.MaskSecrets(reason))
	ans.Safer = report.Printable(report.MaskSecrets(ans.Safer))
	return ans
}

// runAgent prints the snippet that connects an agent to SafeShell. It never
// edits the agent's own settings.
func runAgent(args []string, stdout, stderr io.Writer) int {
	if len(args) != 2 || args[0] != "snippet" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	s, err := agenthook.Snippet(args[1])
	if err != nil {
		fmt.Fprintln(stderr, "safeshell agent:", err)
		return 2
	}
	fmt.Fprint(stdout, s)
	return 0
}
