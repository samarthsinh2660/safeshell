package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func fakeSim(t *testing.T, res sandbox.Result, err error) *sandbox.Spec {
	t.Helper()
	old := simulate
	t.Cleanup(func() { simulate = old })
	var got sandbox.Spec
	simulate = func(_ context.Context, spec sandbox.Spec) (sandbox.Result, error) {
		got = spec
		return res, err
	}
	return &got
}

func TestSimulateReport(t *testing.T) {
	got := fakeSim(t, sandbox.Result{
		ExitCode: 0, Duration: 12 * time.Millisecond, Stdout: "hello\n",
		Diff: sandbox.FileDiff{Changes: []sandbox.Change{
			{Path: "home/me/proj", Kind: sandbox.Deleted, IsDir: true, Files: 4312, Bytes: 2 << 30},
			{Path: "tmp/new.txt", Kind: sandbox.Created, Files: 1, Bytes: 5},
		}},
		Warnings: []sandbox.Warning{{Paths: []string{"/work/proj/cache"}, Text: "/work/proj/cache is not available"}, {Paths: []string{"/var/lib/docker"}, Text: "/var/lib/docker is not available"}},
	}, nil)
	code, out, _ := runSS("simulate", "--cwd", "/work", "--timeout", "5s", "rm", "-rf", "/work/proj")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if got.Line != "rm -rf /work/proj" || got.Cwd != "/work" || got.Timeout != 5*time.Second {
		t.Errorf("spec passed to the sandbox: %+v", got)
	}
	for _, want := range []string{
		"Your real files were not touched", "deleted   4,312 files (2.0 GiB)", "created   1 file",
		"/home/me/proj/  (4,312 files)", "Warning: /work/proj/cache", "1 sandbox note about other folders was hidden", "stdout", "  hello",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestSimulateListsAtMostFifteenChanges(t *testing.T) {
	var cs []sandbox.Change
	for i := 0; i < 20; i++ {
		cs = append(cs, sandbox.Change{Path: "f", Kind: sandbox.Created, Files: 1})
	}
	fakeSim(t, sandbox.Result{Diff: sandbox.FileDiff{Changes: cs}}, nil)
	_, out, _ := runSS("simulate", "x")
	if !strings.Contains(out, "... and 5 more") {
		t.Errorf("long change lists must be cut:\n%s", out)
	}
}

func TestSimulateTimedOutAndJSON(t *testing.T) {
	fakeSim(t, sandbox.Result{TimedOut: true, ExitCode: -1}, nil)
	if _, out, _ := runSS("simulate", "sleep 99"); !strings.Contains(out, "timed out") {
		t.Errorf("timeout not reported:\n%s", out)
	}
	code, out, _ := runSS("simulate", "--json", "x")
	var r sandbox.Result
	if code != 0 || json.Unmarshal([]byte(out), &r) != nil || !r.TimedOut {
		t.Errorf("json output: %d %s", code, out)
	}
}

func TestSimulateErrors(t *testing.T) {
	fakeSim(t, sandbox.Result{}, errors.New("sandbox setup failed: nope"))
	if code, _, e := runSS("simulate", "x"); code != 1 || !strings.Contains(e, "sandbox setup failed") {
		t.Errorf("runner error: %d %q", code, e)
	}
	for _, args := range [][]string{{"simulate"}, {"simulate", "--cwd"}, {"simulate", "--bogus", "x"}, {"simulate", "--timeout", "soon", "x"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestSimulateReportMentionsLimitsAndCutOutput(t *testing.T) {
	var b strings.Builder
	simReport(&b, "rm -rf x", "/w", sandbox.Result{
		Diff:      sandbox.FileDiff{Changes: []sandbox.Change{{Path: "w/x", Kind: sandbox.Deleted, Files: 2, Bytes: 10}}, Truncated: true},
		Truncated: true,
	}, true)
	for _, want := range []string{"count stopped at a limit", "(output cut)", "deleted"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("missing %q in\n%s", want, b.String())
		}
	}
}
