package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

func TestDaemonHandlersRejectBadParamsAndReportTheAI(t *testing.T) {
	useAI(t, fakeAI{}, nil)
	h := daemonHandlers(func() {}, version, func() {})
	ctx := context.Background()
	var e *ipc.Error
	for _, method := range []string{"check", "analyze"} {
		for _, params := range []string{`{"line":""}`, `not json`} {
			if _, err := h[method](ctx, json.RawMessage(params)); !errors.As(err, &e) || e.Code != ipc.CodeBadParams {
				t.Errorf("%s %s: %v", method, params, err)
			}
		}
	}
	got, err := h["status"](ctx, nil)
	if m, ok := got.(map[string]any); err != nil || !ok || m["ai"] != "fake" {
		t.Errorf("%v %v", got, err)
	}
}

func TestDaemonShutdownHandlerStopsTheServerAfterReplying(t *testing.T) {
	stopped := make(chan struct{})
	h := daemonHandlers(func() { close(stopped) }, version, func() {})
	if _, err := h["shutdown"](context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Error("shutdown was never called")
	}
}

func TestDaemonServesUntilItIsToldToStop(t *testing.T) {
	rt := privateDir(t)
	t.Setenv("XDG_RUNTIME_DIR", rt)
	useAI(t, nil, nil)
	done := make(chan int, 1)
	go func() { code, _, _ := runSS("daemon"); done <- code }()
	sock := filepath.Join(rt, "safeshell.sock")
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if code, _, e := runSS("daemon"); code != 1 || !strings.Contains(e, "already running") {
		t.Errorf("a second daemon: %d %q", code, e)
	}
	if code, out, _ := runSS("daemon", "stop"); code != 0 || !strings.Contains(out, "was stopped") {
		t.Errorf("%d %q", code, out)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("the daemon exited with %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Error("the daemon did not stop")
	}
}

// A daemon replaced by another version exits with an error, so systemd's
// Restart=on-failure brings up the installed one.
func TestDaemonOfAnotherVersionExitsWithAnError(t *testing.T) {
	rt := privateDir(t)
	t.Setenv("XDG_RUNTIME_DIR", rt)
	useAI(t, nil, nil)
	done := make(chan int, 1)
	errs := make(chan string, 1)
	go func() { code, _, e := runSS("daemon"); done <- code; errs <- e }()
	sock := filepath.Join(rt, "safeshell.sock")
	for i := 0; i < 500; i++ {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	err := ipc.Call(context.Background(), sock, "analyze", analyzeParams{Line: "ls", Cwd: "/", Version: "another"}, nil)
	if err == nil {
		t.Fatal("a caller of another version must be refused")
	}
	select {
	case code := <-done:
		if e := <-errs; code != 1 || !strings.Contains(e, "different SafeShell version") {
			t.Errorf("exit %d %q", code, e)
		}
	case <-time.After(5 * time.Second):
		t.Error("the daemon did not exit")
	}
}
