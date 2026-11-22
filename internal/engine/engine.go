// Package engine is the application core. It decides what to learn about a
// command (rules, real-system inspection, a sandbox run), combines the answers
// and asks policy what to do. It depends only on interfaces for anything that
// touches the machine; cmd/safeshell supplies the real ones.
package engine

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/classify/rules"
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

// Sizer looks, read-only, at how much data a command could put in the sandbox's
// scratch layer and what the machine can hold. nil means no look is taken.
type Sizer interface {
	Size(ctx context.Context, paths []string) sandbox.Size
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
	Sizer      Sizer // checks a run is not too big for the sandbox; nil to skip the check
	Inspectors []Inspector
	Rules      []policy.Rule     // the admin's team policy; nil when there is none
	Hosts      map[string]string // the user's host patterns and labels (prod, staging); nil when none
	Home       string            // the user's home folder, so a home outside /home is protected
	Protect    []string          // absolute folders the user keeps safe: any change inside them is raised to R3
}

// Analysis is everything learned about one command line.
type Analysis struct {
	Line    string          `json:"line"`
	Verdict domain.Verdict  `json:"verdict"` // from the rules alone
	Effects []domain.Effect `json:"effects"` // observed, inspected and estimated
	Sim     *sandbox.Result `json:"sim,omitempty"`
	// Targets are the files the command is expected to change, read from its
	// arguments; set only when there is no sandbox result. TargetsGap says some
	// operand could not be named (a glob or variable), so the list may be short.
	Targets    []rules.Written `json:"targets,omitempty"`
	TargetsGap bool            `json:"targets_gap,omitempty"`
	SimNote    string          `json:"sim_note,omitempty"` // why it was not simulated, or why that failed
	Level      domain.Level    `json:"level"`              // the final level: rules raised by effects
	Decision   domain.Decision `json:"decision"`
	// Team is what the admin's policy asks of this line; PolicyWarning says the
	// policy file could not be used (set by the caller that loaded it).
	Team          policy.Outcome `json:"team"`
	PolicyWarning string         `json:"policy_warning,omitempty"`
	// Host is what the user's host labels ask of this line.
	Host policy.HostOutcome `json:"host"`
	// Protected is what the protected folders ask of this line.
	Protected policy.ProtectOutcome `json:"protected"`
	// AI is the AI's review of this line; nil when none was made.
	AI *AIReview `json:"ai,omitempty"`
	// Trusted says the person's "always run this here" answer turned the
	// confirmation into Allow (see ApplyTrust).
	Trusted bool `json:"trusted,omitempty"`
}

// AIReview is what an AI review said: who answered, the level it gave and why,
// and whether that raised the level.
type AIReview struct {
	Provider string       `json:"provider"`
	Level    domain.Level `json:"level"`
	Reason   string       `json:"reason"`
	Raised   bool         `json:"raised"`
}

// Analyze rates line, inspects the real system, simulates when the rules say the
// result is worth seeing, and decides.
func (e *Engine) Analyze(ctx context.Context, line, cwd string) Analysis {
	a := Analysis{Line: line, Verdict: classify.Classify(line, e.Home)}

	var cmds []shellparse.Command
	if res, err := shellparse.Parse(line); err == nil {
		for _, p := range res.Pipelines {
			cmds = append(cmds, p.Cmds...)
		}
	}
	for _, inspect := range e.Inspectors {
		a.Effects = append(a.Effects, inspect(cmds)...)
	}

	a.Team = policy.Evaluate(e.Rules, cmds)
	a.Host = policy.EvaluateHosts(e.Hosts, a.Verdict, cmds)
	if len(e.Protect) > 0 {
		a.Protected = policy.EvaluateProtected(e.Protect, changedPaths(cmds, cwd, e.Home))
	}
	a.simulate(ctx, e.Sim, e.Sizer, cmds, cwd)
	if a.Sim == nil {
		paths, complete := rules.WritePaths(cmds, cwd)
		a.Targets, a.TargetsGap = paths, !complete
	}
	a.decide()
	return a
}

// needsSimulation: R2 and R3 commands and anything the rules do not recognise
// are run in the sandbox. R4 is refused from the command text alone, and R0/R1
// are not worth the time.
func needsSimulation(v domain.Verdict) bool {
	return (v.Level >= domain.R2 && v.Level < domain.R4) || len(v.Unclassified) > 0
}

func (a *Analysis) simulate(ctx context.Context, sim Simulator, sizer Sizer, cmds []shellparse.Command, cwd string) {
	switch {
	case !needsSimulation(a.Verdict) && !a.Team.Snapshot: // a snapshot needs the list of files the run changes
		if a.Verdict.Level >= domain.R4 {
			a.SimNote = "not simulated: rated catastrophic from the command text alone"
		}
		return
	case sim == nil:
		a.SimNote = "no sandbox available on this machine (run: safeshell doctor), so file changes are only estimated"
		a.Effects = append(a.Effects, a.estimated("file changes could not be observed"))
		return
	}
	spec := sandbox.Spec{Line: a.Line, Cwd: cwd, Env: EnvFrom(ctx)}
	if sizer != nil {
		size := sizer.Size(ctx, rules.SizingPaths(cmds, cwd, a.Verdict.Unclassified))
		switch sandbox.Place(size) {
		case sandbox.OnDisk:
			spec.UpperOnDisk = true
		case sandbox.Skip:
			a.SimNote = tooBigNote(size)
			a.Effects = append(a.Effects, a.estimated("the files are too large to simulate, so file changes could not be observed"))
			return
		}
	}
	res, err := sim.Simulate(ctx, spec)
	if err != nil {
		a.SimNote = "simulation failed: " + err.Error()
		a.Effects = append(a.Effects, a.estimated("the sandbox run failed, so file changes could not be observed"))
		return
	}
	if incomplete := sandbox.Incomplete(res); incomplete == "" {
		a.Sim = &res
		a.Effects = append(a.Effects, report.FileEffect(res.Diff))
	} else { // no result is kept, so the snapshot falls back to the named files
		a.SimNote = "not simulated: " + incomplete
		a.Effects = append(a.Effects, a.estimated(incomplete))
	}
	if ne, ok := report.NetEffect(res.Net); ok {
		a.Effects = append(a.Effects, ne)
	}
	if re, ok := report.RootEffect(res.RootAsks); ok {
		a.Effects = append(a.Effects, re)
	}
	if why := a.mirrorGap(res.Warnings); why != "" {
		a.Effects = append(a.Effects, a.estimated(why))
	}
	if res.TimedOut {
		a.SimNote = "the sandbox run timed out; changes made before then are shown"
		a.Effects = append(a.Effects, a.estimated(fmt.Sprintf("the run did not finish within %s, so its full effect is unknown", res.Duration.Round(1e8))))
	}
}

// tooBigNote says why a run was not simulated, and what to do about it.
func tooBigNote(s sandbox.Size) string {
	more := ""
	if s.Cut {
		more = "at least "
	}
	return fmt.Sprintf("not simulated: it works on %s%s of files, more than the sandbox can hold in memory (%s), and there is not enough free disk space to hold it there instead. Free some disk space, or check the command yourself before you run it",
		more, report.HumanBytes(s.Bytes), report.HumanBytes(s.RAMLimit))
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
	a.decide()
}

// decide sets the final level and decision: the rules raised by effects, then by
// the team policy, then by the host labels. Each step can only raise.
func (a *Analysis) decide() {
	a.Level = policy.FinalLevel(a.Verdict.Level, a.Effects)
	a.Decision = policy.Decide(a.Level, a.Effects)
	a.Level, a.Decision = a.Team.Apply(a.Level, a.Decision)
	a.Level, a.Decision = a.Host.Apply(a.Level, a.Decision)
	a.Level, a.Decision = a.Protected.Apply(a.Level, a.Decision)
}

// trustCase is the analysis as policy needs it to judge trust.
func (a Analysis) trustCase(set []policy.Trust, cwd string) policy.TrustCase {
	return policy.TrustCase{
		Set: set, Line: a.Line, Cwd: cwd, RuleLevel: a.Verdict.Level, Level: a.Level, Effects: a.Effects,
		Flagged: a.Team != (policy.Outcome{}) || a.Host != (policy.HostOutcome{}) || a.Protected != (policy.ProtectOutcome{}) ||
			a.PolicyWarning != "" || (a.AI != nil && a.AI.Raised) || len(a.Verdict.Unclassified) > 0,
	}
}

// CanTrust says whether the person may be offered "always run this here" for
// this line: it is asking for confirmation and nothing but plain R2 is behind it.
func (a Analysis) CanTrust(cwd string) bool {
	return a.Decision == domain.Confirm && a.trustCase(nil, cwd).Eligible()
}

// ApplyTrust turns a confirmation into Allow when the person trusted this exact
// line in this folder. Call it last, after any AI review: it never touches a
// block, and a case that anything raised is left as it was.
func (a *Analysis) ApplyTrust(set []policy.Trust, cwd string) {
	if a.Decision == domain.Confirm && policy.DecideTrusted(a.Decision, a.trustCase(set, cwd)) == domain.Allow {
		a.Decision, a.Trusted = domain.Allow, true
	}
}

// changedPaths are the files and folders the commands change, from their
// arguments alone. ~ stands for home, and a word with a glob or variable is
// cut back to the folder before it ("~/thesis/$f" is "~/thesis/"), so a
// pattern cannot slip past a protected folder. A path that cannot be named at
// all is left out.
func changedPaths(cmds []shellparse.Command, cwd, home string) []string {
	cut := func(w string) string {
		switch {
		case w == "~":
			return home
		case strings.HasPrefix(w, "~/"):
			w = home + w[1:]
		}
		if i := strings.IndexAny(w, "*?[]{}$`"); i >= 0 {
			w = w[:i]
			w = w[:strings.LastIndex(w, "/")+1]
			if w == "" {
				w = "./"
			}
		}
		return w
	}
	expanded := make([]shellparse.Command, len(cmds))
	for i, c := range cmds {
		c.Args = slices.Clone(c.Args)
		for j, a := range c.Args {
			c.Args[j] = cut(a)
		}
		c.Redirs = slices.Clone(c.Redirs)
		for j, r := range c.Redirs {
			c.Redirs[j].Target = cut(r.Target)
		}
		expanded[i] = c
	}
	var out []string
	written, _ := rules.WritePaths(expanded, cwd)
	for _, w := range written {
		out = append(out, cmp.Or(w.Into, w.Path))
	}
	return out
}
