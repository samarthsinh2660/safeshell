package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

const (
	// maxDoSteps is how many commands one task may run before the agent stops.
	maxDoSteps = 8
	// maxStepOutput is how much of a command's output is kept for the model.
	maxStepOutput = 4000
)

// capped keeps the first maxStepOutput bytes written to it and drops the rest.
type capped struct{ buf bytes.Buffer }

func (c *capped) Write(p []byte) (int, error) {
	if room := maxStepOutput - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}
	return len(p), nil
}

// runDo is `safeshell do <task>`: the assistant works through a task one
// command at a time. Each command is shown and checked like a typed one, asks
// the person (autorun does not apply, because the model has read files and
// what they say is untrusted) and runs only on yes. What it printed
// (secrets masked) is shown to the model so it can choose the next command.
func runDo(args []string, stdout, stderr io.Writer, color bool) int {
	args, ok := endOptions("do", args, stderr)
	if !ok {
		return 2
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	b, err := backend()
	if err != nil || b == nil {
		fmt.Fprintln(stderr, "SafeShell AI is off.", aiOffHint)
		return 1
	}
	if !strings.HasPrefix(b.Name(), "local:") {
		fmt.Fprintln(stderr, "The agent shows the model what your commands print, so it only works with a local model that stays on this computer. Choose one with: safeshell models")
		return 1
	}
	task, cwd := strings.Join(args, " "), cwdOr("")
	env := gatherEnv(cwd)
	env.Agent = true
	seen := map[string]bool{}
	for range maxDoSteps {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		r, err := nl.TranslateWith(ctx, b, task, nil, env)
		cancel()
		switch {
		case errors.Is(err, nl.ErrNoCommand):
			fmt.Fprintln(stderr, "Done: the assistant had nothing more to run.")
			return 0
		case err != nil:
			fmt.Fprintln(stderr, "safeshell do:", firstErrLine(err))
			return 1
		case r.Question != "":
			fmt.Fprintf(stderr, "The AI asks: %s\nNothing more was run. Ask again with more detail.\n", report.Printable(r.Question))
			return 1
		case r.Command == "":
			fmt.Fprintln(stderr, "Done:", report.Printable(r.Answer))
			return 0
		case seen[r.Command]:
			fmt.Fprintln(stderr, "Stopped: the assistant repeated a command. Ask again with more detail.")
			return 1
		}
		seen[r.Command] = true
		step, stop := doStep(r, cwd, stdout, stderr, color)
		env.Steps = append(env.Steps, step)
		if stop {
			return 1
		}
	}
	fmt.Fprintf(stderr, "Stopped after %d steps. Ask again to carry on.\n", maxDoSteps)
	return 1
}

// doStep shows, checks and (when approved) runs one proposed command. It
// reports what the model should see, and whether the whole task must stop
// because the person said no or the command was blocked.
func doStep(r nl.Result, cwd string, stdout, stderr io.Writer, color bool) (step nl.Step, stop bool) {
	step.Command = r.Command
	if changesShell(r.Command) {
		step.Output = "not run: the agent cannot change your shell, so use a command that does not"
		fmt.Fprintf(stderr, "Skipped: %s would change your shell, which the agent cannot do.\n", report.Printable(r.Command))
		return step, false
	}
	var approved strings.Builder
	if proposeAndGuard(r, cwd, &approved, stderr, color, fromTask) != 0 {
		step.Output = "not run: the person declined it or it was blocked"
		return step, true
	}
	var out capped
	err := runCommand(strings.TrimSpace(approved.String()), cwd, io.MultiWriter(stdout, &out), io.MultiWriter(stderr, &out))
	step.Output = report.MaskSecrets(out.buf.String())
	if err != nil {
		step.Output = "failed (" + err.Error() + ")\n" + step.Output
	}
	return step, false
}
