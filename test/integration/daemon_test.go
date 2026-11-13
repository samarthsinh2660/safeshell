package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

func startDaemon(t *testing.T) string {
	t.Helper()
	rt := t.TempDir()
	if err := os.Chmod(rt, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ssBin, "daemon")
	cmd.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+rt)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _, _ = cmd.Process.Wait() })
	sock := filepath.Join(rt, "safeshell.sock")
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if err := ipc.Call(context.Background(), sock, "ping", nil, nil); err == nil {
			return sock
		}
	}
	t.Fatal("the daemon did not start")
	return ""
}

func TestDaemonAnalysesLikeTheCommandLineAndSeesTheCallersEnvironment(t *testing.T) {
	needSandbox(t)
	sock := startDaemon(t)
	d := fixture(t)
	var a engine.Analysis
	err := ipc.Call(context.Background(), sock, "analyze", map[string]any{"line": "rm -rf proj", "cwd": d}, &a)
	if err != nil || a.Level.String() != "R3" || a.Decision.String() != "block" || len(a.Effects) == 0 || a.Effects[0].Evidence.String() != "OBSERVED" {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := os.Stat(filepath.Join(d, "proj", "a.txt")); err != nil {
		t.Error("the daemon's analysis touched the real files")
	}

	// The command must run with the caller's environment, not the daemon's.
	env := append(os.Environ(), "SS_ENVPROBE=from-the-callers-shell")
	err = ipc.Call(context.Background(), sock, "analyze", map[string]any{
		"line": `python3 -c "import os; print(os.environ.get('SS_ENVPROBE'))"`, "cwd": d, "env": env}, &a)
	if err != nil || a.Sim == nil || !strings.Contains(a.Sim.Stdout, "from-the-callers-shell") {
		t.Errorf("environment not passed through: %v %+v", err, a.Sim)
	}

	if err := ipc.Call(context.Background(), sock, "shutdown", nil, nil); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(sock); err != nil {
			return
		}
	}
	t.Error("the socket was not removed on shutdown")
}

func TestDaemonSocketIsPrivate(t *testing.T) {
	needSandbox(t)
	sock := startDaemon(t)
	st, err := os.Stat(sock)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode %v %v", st, err)
	}
}
