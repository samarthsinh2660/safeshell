// Package engine is the application core. It decides what to learn about a
// command (rules, real-system inspection, a sandbox run), combines the answers
// and asks policy what to do. It depends only on interfaces for anything that
// touches the machine; cmd/safeshell supplies the real ones.
package engine

import (
	"context"
	"fmt"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/policy"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Simulator runs a command in isolation and reports what it changed.
type Simulator interface {
	Simulate(ctx context.Context, spec sandbox.Spec) (sandbox.Result, error)
}

// Inspector reads the real system, never changing it, to find the effects a
// file sandbox cannot see (which processes a kill hits, which disk a dd erases).
type Inspector func(cmds []shellparse.Command) []domain.Effect

type envKey struct{}

// WithEnv attaches the caller's environment, which the simulated command
// should see (PATH, virtualenvs, ...) rather than that of a long-running daemon.
func WithEnv(ctx context.Context, env []string) context.Context {
	return context.WithValue(ctx, envKey{}, env)
}

// EnvFrom is the environment attached with WithEnv, or nil when there is none.
func EnvFrom(ctx context.Context) []string {
	env, _ := ctx.Value(envKey{}).([]string)
	return env
}

type callerKey struct{}

// WithCaller attaches the pid of the process that asked, whose parent shell
// must be protected from a kill, rather than that of a long-running daemon.
func WithCaller(ctx context.Context, pid int) context.Context {
	return context.WithValue(ctx, callerKey{}, pid)
}

// CallerFrom is the pid attached with WithCaller, or 0 when there is none.
func CallerFrom(ctx context.Context) int {
	pid, _ := ctx.Value(callerKey{}).(int)
	return pid
}

// Engine analyses commands. A nil Sim means no sandbox is available here.
type Engine struct {
	Sim        Simulator
	Inspectors []Inspector
}

// Analysis is everything learned about one command line.
type Analysis struct {
	Line     string          `json:"line"`
	Verdict  domain.Verdict  `json:"verdict"` // from the rules alone
	Effects  []domain.Effect `json:"effects"` // observed, inspected and estimated
	Sim      *sandbox.Result `json:"sim,omitempty"`
	SimNote  string          `json:"sim_note,omitempty"` // why it was not simulated, or why that failed
	Level    domain.Level    `json:"level"`              // the final level: rules raised by effects
	Decision domain.Decision `json:"decision"`
}

// Analyze rates line, inspects the real system, simulates when the rules say the
// result is worth seeing, and decides.
func (e *Engine) Analyze(ctx context.Context, line, cwd string) Analysis {
	a := Analysis{Line: line, Verdict: classify.Classify(line)}

	var cmds []shellparse.Command
	if res, err := shellparse.Parse(line); err == nil {
		for _, p := range res.Pipelines {
			cmds = append(cmds, p.Cmds...)
		}
	}
	for _, inspect := range e.Inspectors {
		a.Effects = append(a.Effects, inspect(cmds)...)
	}

	a.simulate(ctx, e.Sim, cwd)

	a.Level = policy.FinalLevel(a.Verdict.Level, a.Effects)
	a.Decision = policy.Decide(a.Level, a.Effects)
	return a
}

// needsSimulation: R2 and R3 commands and anything the rules do not recognise
// are run in the sandbox. R4 is refused from the command text alone, and R0/R1
// are not worth the time.
func needsSimulation(v domain.Verdict) bool {
	return (v.Level >= domain.R2 && v.Level < domain.R4) || len(v.Unclassified) > 0
}

func (a *Analysis) simulate(ctx context.Context, sim Simulator, cwd string) {
	switch {
	case !needsSimulation(a.Verdict):
		if a.Verdict.Level >= domain.R4 {
			a.SimNote = "not simulated: rated catastrophic from the command text alone"
		}
		return
	case sim == nil:
		a.SimNote = "no sandbox available on this machine (run: safeshell doctor), so file changes are only estimated"
		a.Effects = append(a.Effects, a.estimated("file changes could not be observed"))
		return
	}
	res, err := sim.Simulate(ctx, sandbox.Spec{Line: a.Line, Cwd: cwd, Env: EnvFrom(ctx)})
	if err != nil {
		a.SimNote = "simulation failed: " + err.Error()
		a.Effects = append(a.Effects, a.estimated("the sandbox run failed, so file changes could not be observed"))
		return
	}
	a.Sim = &res
	a.Effects = append(a.Effects, report.FileEffect(res.Diff))
	if ne, ok := report.NetEffect(res.Net); ok {
		a.Effects = append(a.Effects, ne)
	}
	if why := a.mirrorGap(res.Warnings); why != "" {
		a.Effects = append(a.Effects, a.estimated(why))
	}
	if res.TimedOut {
		a.SimNote = "the sandbox run timed out; changes made before then are shown"
		a.Effects = append(a.Effects, a.estimated(fmt.Sprintf("the run did not finish within %s, so its full effect is unknown", res.Duration.Round(1e8))))
	}
}

// mirrorGap explains why an empty diff cannot be trusted for a command the
// rules did not recognise, or returns "" when it can. A warning is a part of the
// filesystem the sandbox could not see; only where the user can write could a
// change there have gone unrecorded.
func (a *Analysis) mirrorGap(ws []sandbox.Warning) string {
	if len(a.Verdict.Unclassified) == 0 {
		return ""
	}
	for _, w := range ws {
		if len(w.Writable) > 0 {
			return "part of a folder you can write to was not visible in the sandbox, so \"no changes\" can't be trusted"
		}
	}
	return ""
}

// estimated is an effect with no proof, which keeps the command from running unattended.
func (a *Analysis) estimated(why string) domain.Effect {
	return domain.Effect{Kind: domain.EffectFS, Evidence: domain.Estimated, Summary: why, MinLevel: a.Verdict.Level}
}

// AddEffect records a further finding (such as an AI review) and decides again.
// Effects can only raise the level, never lower it.
func (a *Analysis) AddEffect(e domain.Effect) {
	a.Effects = append(a.Effects, e)
	a.Level = policy.FinalLevel(a.Verdict.Level, a.Effects)
	a.Decision = policy.Decide(a.Level, a.Effects)
}
