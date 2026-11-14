package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

// killLevel is the risk of `kill -9 pid` when the process that asked is caller
// (0 means this process, as when run directly).
func killLevel(caller, pid int) domain.Level {
	ctx := context.Background()
	if caller > 0 {
		ctx = engine.WithCaller(ctx, caller)
	}
	return analyze(ctx, fmt.Sprintf("kill -9 %d", pid), "/").Level
}

func TestKillingTheCallersParentIsProtectedThroughTheDaemonToo(t *testing.T) {
	// shell stands for the user's shell and caller for the guard it started.
	// The daemon is neither: it must protect the caller's parents, not its own.
	shell := exec.Command("sh", "-c", "sleep 30 & echo $!; wait")
	out, err := shell.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := shell.Start(); err != nil {
		t.Fatal(err)
	}
	var caller int
	if _, err := fmt.Fscan(out, &caller); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { syscall.Kill(caller, syscall.SIGKILL); shell.Process.Kill(); shell.Wait() })

	if lvl := killLevel(caller, shell.Process.Pid); lvl != domain.R4 {
		t.Errorf("kill -9 <the caller's shell> through the daemon: %s, want R4", lvl)
	}
	if lvl := killLevel(0, shell.Process.Pid); lvl >= domain.R4 {
		t.Errorf("run directly, this process is the caller and a stranger's shell is not its parent: %s", lvl)
	}
}

func TestDaemonAnalyzeUsesTheCallersPid(t *testing.T) {
	var got int
	old := analyze
	t.Cleanup(func() { analyze = old })
	analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
		got = engine.CallerFrom(ctx)
		return engine.Analysis{}
	}
	h := daemonHandlers(func() {})["analyze"]
	if _, err := h(context.Background(), json.RawMessage(`{"line":"kill -9 1","cwd":"/","pid":4242}`)); err != nil || got != 4242 {
		t.Errorf("caller pid %d, err %v", got, err)
	}
}

func TestAnalyzeFastSendsItsOwnPidToTheDaemon(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	old := socketPath
	t.Cleanup(func() { socketPath = old })
	path := filepath.Join(privateDir(t), "d.sock")
	socketPath = func() string { return path }
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	var got analyzeParams
	h := map[string]ipc.Handler{"analyze": func(_ context.Context, p json.RawMessage) (any, error) {
		return engine.Analysis{}, json.Unmarshal(p, &got)
	}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&ipc.Server{Handlers: h}).Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	analyzeFast(context.Background(), "x", "/", io.Discard)
	if got.Pid != os.Getpid() {
		t.Errorf("sent pid %d, want %d", got.Pid, os.Getpid())
	}
}
