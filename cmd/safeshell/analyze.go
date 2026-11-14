package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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

// gitRunner runs the read-only repository queries in the caller's environment.
// It is a test seam: tests replace it with a fake.
var gitRunner = realGitRunner

// toolRunner runs the read-only docker and kubectl queries in the caller's
// folder and environment, so DOCKER_HOST, KUBECONFIG and the like are the
// user's. It is a test seam: tests replace it with a fake.
var toolRunner = realToolRunner

// inspectBudget is the most time one inspector may spend asking other programs.
const inspectBudget = 15 * time.Second

// analyze is replaced in tests.
var analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
	env := callerEnv(ctx)
	inspectRepo := func(cmds []shellparse.Command) []domain.Effect {
		if envHas(env, "GIT_DIR", "GIT_WORK_TREE") {
			return nil // the repository is not the one in the folder, so it is not looked at
		}
		bctx, cancel := context.WithTimeout(ctx, inspectBudget)
		defer cancel()
		run := func(dir string, args ...string) (string, error) { return gitRunner(bctx, env, dir, args...) }
		return effects.InspectGit(run, cwd, cmds)
	}
	// Each of the two gets its own budget, which also ends with the request.
	tool := func(f func(effects.Runner, []shellparse.Command) []domain.Effect) engine.Inspector {
		return func(cmds []shellparse.Command) []domain.Effect {
			bctx, cancel := context.WithTimeout(ctx, inspectBudget)
			defer cancel()
			return f(func(name string, args ...string) (string, error) { return toolRunner(bctx, env, cwd, name, args...) }, cmds)
		}
	}
	e := &engine.Engine{Sim: realSandbox{}, Inspectors: []engine.Inspector{inspectProcesses, inspectDisks, inspectPackages, inspectServices, inspectRepo,
		tool(effects.InspectDocker), tool(effects.InspectKubectl)}}
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
	return runLimited(context.Background(), toolTimeout, append(os.Environ(), "LC_ALL=C", "LANG=C"), "", name, args...)
}

const toolTimeout = 8 * time.Second

// runLimited runs a program, found through the PATH in env, in dir and returns
// its output, giving up after timeout or when ctx ends. The program and
// everything it starts run in their own process group, which is killed on
// timeout and again when the program has finished, so nothing outlives the
// query; a child that keeps the output open cannot hold the call longer than a
// second more.
func runLimited(ctx context.Context, timeout time.Duration, env []string, dir, name string, args ...string) (string, error) {
	path, err := lookPath(env, name)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env = dir, env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	defer func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	}()
	out, err := cmd.Output()
	return string(out), err
}

// lookPath finds name in the PATH given in env (the requesting shell's, not
// this process's), skipping relative entries so the current folder is never searched.
func lookPath(env []string, name string) (string, error) {
	if strings.Contains(name, "/") {
		return name, nil
	}
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			for _, d := range filepath.SplitList(v) {
				p := filepath.Join(d, name)
				if fi, err := os.Stat(p); filepath.IsAbs(d) && err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0 {
					return p, nil
				}
			}
		}
	}
	return "", fmt.Errorf("%s was not found in the PATH of your shell", name)
}

// callerEnv is the environment of the shell that asked, which the daemon
// receives with the request, or this process's own when there is none.
func callerEnv(ctx context.Context) []string {
	if env := engine.EnvFrom(ctx); env != nil {
		return env
	}
	return os.Environ()
}

// envHas reports whether env sets any of the named variables.
func envHas(env []string, names ...string) bool {
	for _, e := range env {
		k, _, _ := strings.Cut(e, "=")
		for _, n := range names {
			if k == n {
				return true
			}
		}
	}
	return false
}

// realGitRunner runs one read-only git query in dir. It never prompts, never
// takes the optional index lock, ignores system-wide git settings and
// attributes, and drops the variables that name a pager or an external diff.
func realGitRunner(ctx context.Context, env []string, dir string, args ...string) (string, error) {
	var clean []string
	for _, e := range env {
		if k, _, _ := strings.Cut(e, "="); k != "GIT_EXTERNAL_DIFF" && k != "GIT_PAGER" && k != "PAGER" {
			clean = append(clean, e)
		}
	}
	clean = append(clean, "LC_ALL=C", "LANG=C", "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_ATTR_NOSYSTEM=1", "GIT_NO_LAZY_FETCH=1")
	return runLimited(ctx, toolTimeout, clean, dir, "git", args...)
}

// realToolRunner runs a read-only query in dir with the caller's environment and a plain locale.
func realToolRunner(ctx context.Context, env []string, dir, name string, args ...string) (string, error) {
	return runLimited(ctx, toolTimeout, append(env[:len(env):len(env)], "LC_ALL=C", "LANG=C"), dir, name, args...)
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
