package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

func TestAnalyzeFastPassesTheEnvironmentToTheDaemonUnchanged(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	t.Setenv("MY_VAR", "1")
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
	if !slices.Contains(got.Env, "MY_VAR=1") {
		t.Errorf("forwarded %v", got.Env)
	}
}

func TestAnalyzeFastIgnoresASocketThatIsNotOursAndWarns(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	local := sampleAnalysis()
	local.Line = "ran-locally"
	fakeAnalyze(t, local)
	fake := filepath.Join(privateDir(t), "fake.sock")
	if err := os.WriteFile(fake, nil, 0o600); err != nil { // a file, not a socket
		t.Fatal(err)
	}
	old := socketPath
	t.Cleanup(func() { socketPath = old })
	socketPath = func() string { return fake }
	var warn strings.Builder
	if a := analyzeFast(context.Background(), "x", "/", &warn); a.Line != "ran-locally" {
		t.Errorf("must analyse directly: %q", a.Line)
	}
	if !strings.Contains(warn.String(), fake+" is not your SafeShell daemon") || !strings.Contains(warn.String(), "Delete it") {
		t.Errorf("warning: %q", warn.String())
	}
}

func TestDaemonRefusesToStartWithoutARuntimeFolder(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if code, _, e := runSS("daemon"); code != 1 || !strings.Contains(e, "XDG_RUNTIME_DIR") {
		t.Errorf("%d %q", code, e)
	}
}

func TestDaemonStopWarnsAboutASocketThatIsNotOurs(t *testing.T) {
	rt := privateDir(t)
	if err := os.WriteFile(filepath.Join(rt, "safeshell.sock"), nil, 0o600); err != nil { // a file, not a socket
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", rt)
	if code, out, e := runSS("daemon", "stop"); code != 1 || out != "" || !strings.Contains(e, "is not your SafeShell daemon") {
		t.Errorf("%d %q %q", code, out, e)
	}
}

func TestAnalyzeFastFallsBackToLocalAnalysisWhenTheDaemonIsAnotherVersion(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	local := sampleAnalysis()
	local.Line = "ran-locally"
	fakeAnalyze(t, local)
	old := socketPath
	t.Cleanup(func() { socketPath = old })
	path := filepath.Join(privateDir(t), "d.sock")
	socketPath = func() string { return path }
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		(&ipc.Server{Handlers: daemonHandlers(func() { cancel(); close(exited) }, "an-older-version")}).Serve(ctx, l)
		close(done)
	}()
	t.Cleanup(func() { cancel(); <-done })
	if a := analyzeFast(context.Background(), "x", "/", io.Discard); a.Line != "ran-locally" {
		t.Errorf("must analyse in this process: %q", a.Line)
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Error("the old daemon did not exit")
	}
}

func TestSocketGoneWaitsThenGivesUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if socketGone(p, 50*time.Millisecond) {
		t.Error("the file is still there")
	}
	go func() { time.Sleep(60 * time.Millisecond); os.Remove(p) }()
	if !socketGone(p, 5*time.Second) {
		t.Error("the file was removed")
	}
}

func TestDaemonStopSaysStillStoppingWhenTheSocketStays(t *testing.T) {
	rt := privateDir(t)
	t.Setenv("XDG_RUNTIME_DIR", rt)
	old := stopWait
	t.Cleanup(func() { stopWait = old })
	stopWait = 100 * time.Millisecond
	path := filepath.Join(rt, "safeshell.sock")
	l, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	// A daemon whose shutdown does nothing keeps its socket.
	go func() { (&ipc.Server{Handlers: daemonHandlers(func() {}, version)}).Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	if code, out, _ := runSS("daemon", "stop"); code != 1 || !strings.Contains(out, "still stopping") {
		t.Errorf("%d %q", code, out)
	}
}
