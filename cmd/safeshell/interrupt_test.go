package main

import (
	"context"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// interrupt sends this process a Ctrl-C and waits until ctx has noticed it, as
// the real simulation would while a command runs in the sandbox.
func interrupt(t *testing.T, ctx context.Context) {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	<-ctx.Done()
}

func TestGuardCtrlCDuringAnalysisCancelsAndPrintsNothingToRun(t *testing.T) {
	old := analyze
	t.Cleanup(func() { analyze = old })
	analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
		interrupt(t, ctx)
		return engine.Analysis{Line: line}
	}
	answer(t, "y\n") // a cancelled guard must not go on to ask or run anything
	code, out, errOut := runSS("guard", "--", "rm -rf ~/projects")
	if code != 1 || out != "" || !strings.Contains(errOut, "Nothing was run") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestAnalyzeCtrlCStopsWithTheInterruptedStatus(t *testing.T) {
	old := analyze
	t.Cleanup(func() { analyze = old })
	analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
		interrupt(t, ctx)
		return engine.Analysis{Line: line}
	}
	code, out, errOut := runSS("analyze", "rm -rf x")
	if code != exitInterrupted || out != "" || !strings.Contains(errOut, "Nothing was run") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestSimulateCtrlCStopsWithTheInterruptedStatus(t *testing.T) {
	old := simulate
	t.Cleanup(func() { simulate = old })
	simulate = func(ctx context.Context, spec sandbox.Spec) (sandbox.Result, error) {
		interrupt(t, ctx)
		return sandbox.Result{}, ctx.Err()
	}
	code, out, errOut := runSS("simulate", "rm -rf x")
	if code != exitInterrupted || out != "" || !strings.Contains(errOut, "Nothing was run") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}
