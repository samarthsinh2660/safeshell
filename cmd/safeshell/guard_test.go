package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func answer(t *testing.T, s string) {
	t.Helper()
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	guardInput = func() io.Reader { return strings.NewReader(s) }
}

func TestGuardAllowedCommandPrintsItWithoutAsking(t *testing.T) {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	answer(t, "")
	code, out, errOut := runSS("guard", "--cwd", "/work", "--", "ls", "-l")
	if code != 0 || out != "ls -l\n" || errOut != "" {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestGuardPassesCwdAndLine(t *testing.T) {
	got := fakeAnalyze(t, sampleAnalysis())
	answer(t, "c\n")
	runSS("guard", "--cwd", "/work", "--", "rm -rf x")
	if got[0] != "rm -rf x" || got[1] != "/work" {
		t.Errorf("analyze got %v", *got)
	}
}

func TestGuardBlockedCancelledByDefault(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	answer(t, "\n")
	code, out, errOut := runSS("guard", "--", "rm -rf ~/projects")
	if code != 1 || out != "" || !strings.Contains(errOut, "Nothing was run") || !strings.Contains(errOut, "BLOCK") {
		t.Errorf("code %d out %q err %q", code, out, errOut)
	}
}

func TestGuardSaferOptionReplacesTheCommand(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	answer(t, "1\n")
	code, out, _ := runSS("guard", "--", "rm -rf ~/projects")
	if code != 0 || out != "trash-put ~/projects\n" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestGuardDoItAnywayNeedsConfirmation(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	answer(t, "d\nyes\n")
	if code, out, _ := runSS("guard", "--", "rm -rf ~/projects"); code != 0 || out != "rm -rf ~/projects\n" {
		t.Errorf("code %d out %q", code, out)
	}
	answer(t, "d\nnope\n")
	if code, out, _ := runSS("guard", "--", "rm -rf ~/projects"); code != 1 || out != "" {
		t.Errorf("code %d out %q", code, out)
	}
}

func TestGuardUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"guard"}, {"guard", "--"}, {"guard", "--bogus", "x"}, {"hook"}, {"hook", "fish"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestHookPrintsScript(t *testing.T) {
	code, out, _ := runSS("hook", "bash")
	if code != 0 || !strings.Contains(out, "-x '\"\\C-x\\C-s\": __ss_guard'") {
		t.Errorf("code %d out %q", code, out)
	}
	if _, out, _ := runSS("hook", "zsh"); !strings.Contains(out, "zle -N ${__ss_w}") {
		t.Errorf("zsh: %q", out)
	}
}

func TestMain(m *testing.M) { // never write the real audit log from tests
	d, _ := os.MkdirTemp("", "ss-audit")
	auditPath = func() string { return filepath.Join(d, "audit.jsonl") }
	backend = func() (ai.Backend, error) { return nil, nil }                 // tests never reach a real model
	socketPath = func() string { return filepath.Join(d, "no-daemon.sock") } // nor a real daemon
	code := m.Run()
	os.RemoveAll(d)
	os.Exit(code)
}

func lastAudit(t *testing.T) audit.Entry {
	t.Helper()
	b, err := os.ReadFile(auditPath())
	if err != nil {
		t.Fatal(err)
	}
	ls := strings.Split(strings.TrimSpace(string(b)), "\n")
	var e audit.Entry
	if err := json.Unmarshal([]byte(ls[len(ls)-1]), &e); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestGuardLogsEveryOutcome(t *testing.T) {
	for _, tc := range []struct{ in, outcome, run string }{
		{"c\n", "cancelled", ""},
		{"1\n", "replaced", "trash-put ~/projects"},
		{"d\nyes\n", "ran", ""},
	} {
		fakeAnalyze(t, sampleAnalysis())
		answer(t, tc.in)
		runSS("guard", "--", "rm -rf ~/projects")
		e := lastAudit(t)
		if e.Outcome != tc.outcome || e.Run != tc.run || e.Level != "R3" || e.Decision != "block" || e.Line != "rm -rf ~/projects" {
			t.Errorf("%q: %+v", tc.in, e)
		}
	}
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	fakeAnalyze(t, a)
	runSS("guard", "--", "ls")
	if e := lastAudit(t); e.Outcome != "allowed" {
		t.Errorf("%+v", e)
	}
}

func TestInstallAndUninstallEditOnlyTheHookBlock(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	rc := filepath.Join(home, ".bashrc")
	os.WriteFile(rc, []byte("export KEEP=1\n"), 0o644)
	if code, out, _ := runSS("install"); code != 0 || !strings.Contains(out, "Added the SafeShell hook") {
		t.Fatalf("%d %q", code, out)
	}
	if b, _ := os.ReadFile(rc); !strings.Contains(string(b), "hook bash") || !strings.HasPrefix(string(b), "export KEEP=1\n") {
		t.Errorf("%q", b)
	}
	if _, out, _ := runSS("install"); !strings.Contains(out, "already in") {
		t.Errorf("second install: %q", out)
	}
	if code, out, _ := runSS("uninstall"); code != 0 || !strings.Contains(out, "Removed") {
		t.Fatalf("%d %q", code, out)
	}
	if b, _ := os.ReadFile(rc); string(b) != "export KEEP=1\n" {
		t.Errorf("uninstall changed more than the block: %q", b)
	}
	if _, out, _ := runSS("uninstall"); !strings.Contains(out, "was not in") {
		t.Errorf("%q", out)
	}
}

func TestInstallRefusesUnsupportedShellsAndBadOptions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/usr/bin/fish")
	if code, _, e := runSS("install"); code != 1 || !strings.Contains(e, "fish") {
		t.Errorf("%d %q", code, e)
	}
	if code, _, _ := runSS("install", "--bogus"); code != 2 {
		t.Error("usage")
	}
}

func withDaemon(t *testing.T, a engine.Analysis, seenEnv *[]string) {
	t.Helper()
	old, oldPath := analyze, socketPath
	t.Cleanup(func() { analyze, socketPath = old, oldPath })
	analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
		if e, ok := ctx.Value(envProbe{}).([]string); ok && seenEnv != nil {
			*seenEnv = e
		}
		return a
	}
	path := filepath.Join(t.TempDir(), "d.sock")
	socketPath = func() string { return path }
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&ipc.Server{Handlers: daemonHandlers(cancel)}).Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
}

type envProbe struct{}

func TestAnalyzeFastUsesTheDaemonWhenItIsRunningAndFallsBackWhenNot(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	local := sampleAnalysis()
	local.Line = "ran-locally"
	fakeAnalyze(t, local)
	socketPath = func() string { return filepath.Join(t.TempDir(), "nothing.sock") }
	if a := analyzeFast(context.Background(), "x", "/"); a.Line != "ran-locally" {
		t.Errorf("no daemon: %q", a.Line)
	}
	remote := sampleAnalysis()
	remote.Line = "ran-in-daemon"
	withDaemon(t, remote, nil)
	got := analyzeFast(context.Background(), "x", "/")
	if got.Line != "ran-in-daemon" || got.Level != domain.R3 || got.Decision != domain.Block || len(got.Effects) != 1 || got.Effects[0].Evidence != domain.Observed {
		t.Errorf("through the daemon: %+v", got)
	}
	t.Setenv("SAFESHELL_NODAEMON", "1")
	fakeAnalyze(t, local)
	if a := analyzeFast(context.Background(), "x", "/"); a.Line != "ran-locally" {
		t.Error("SAFESHELL_NODAEMON must bypass the daemon")
	}
}

func TestDaemonMethods(t *testing.T) {
	withDaemon(t, sampleAnalysis(), nil)
	var ping struct{ Protocol int }
	if err := ipc.Call(context.Background(), socketPath(), "ping", nil, &ping); err != nil || ping.Protocol != ipc.Protocol {
		t.Errorf("%+v %v", ping, err)
	}
	var v domain.Verdict
	if err := ipc.Call(context.Background(), socketPath(), "check", map[string]string{"line": "rm -rf ~/x"}, &v); err != nil || v.Level != domain.R3 {
		t.Errorf("check: %v %v", v.Level, err)
	}
	var e *ipc.Error
	if err := ipc.Call(context.Background(), socketPath(), "check", map[string]string{}, nil); !errors.As(err, &e) || e.Code != ipc.CodeBadParams {
		t.Errorf("empty line: %v", err)
	}
	if err := ipc.Call(context.Background(), socketPath(), "status", nil, nil); err != nil {
		t.Errorf("status: %v", err)
	}
}

func TestDaemonCommandUsageAndStopWhenNotRunning(t *testing.T) {
	socketPath = func() string { return "/nonexistent/none.sock" }
	t.Cleanup(func() { socketPath = ipc.SocketPath })
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if code, out, _ := runSS("daemon", "stop"); code != 0 || !strings.Contains(out, "not running") {
		t.Errorf("%d %q", code, out)
	}
	if code, _, _ := runSS("daemon", "bogus"); code != 2 {
		t.Error("usage")
	}
}
