package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

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
	// Until the child has become sleep its /proc entry can be mid-exec, with no command line yet.
	for i := 0; i < 250; i++ {
		if b, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", caller)); strings.HasPrefix(string(b), "sleep\x00") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if lvl := killLevel(caller, shell.Process.Pid); lvl != domain.R4 {
		t.Errorf("kill -9 <the caller's shell> through the daemon: %s, want R4", lvl)
	}
	if lvl := killLevel(0, shell.Process.Pid); lvl >= domain.R4 {
		t.Errorf("run directly, this process is the caller and a stranger's shell is not its parent: %s", lvl)
	}
}

func TestTheDaemonProtectsTheShellOfTheProcessThatAsked(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	oldSock, oldAnalyze := socketPath, analyze
	t.Cleanup(func() { socketPath, analyze = oldSock, oldAnalyze })
	path := filepath.Join(privateDir(t), "d.sock")
	socketPath = func() string { return path }
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	got := -1
	analyze = func(ctx context.Context, line, cwd string) engine.Analysis {
		got = engine.CallerFrom(ctx)
		return engine.Analysis{Line: line}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { (&ipc.Server{Handlers: daemonHandlers(func() {})}).Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	// The caller is whoever the kernel says is on the other end of the socket; a pid
	// written into the request is ignored, so no client can choose whose shell is protected.
	var a engine.Analysis
	if err := ipc.Call(context.Background(), path, "analyze", map[string]any{"line": "kill -9 1", "cwd": "/", "pid": 4242}, &a); err != nil {
		t.Fatal(err)
	}
	if got != os.Getpid() {
		t.Errorf("caller pid %d, want this process %d", got, os.Getpid())
	}
}
