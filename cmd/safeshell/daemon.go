package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

type analyzeParams struct {
	Line string   `json:"line"`
	Cwd  string   `json:"cwd"`
	Env  []string `json:"env,omitempty"`
}

// runDaemon serves analyses over a private Unix socket, so the shell hook does
// not start a new process (and reload a model) for every Enter.
func runDaemon(args []string, stdout, stderr io.Writer) int {
	path := ipc.SocketPath()
	if len(args) == 1 && args[0] == "stop" {
		if err := ipc.Call(context.Background(), path, "shutdown", nil, nil); err != nil {
			fmt.Fprintln(stdout, "The SafeShell daemon is not running.")
			return 0
		}
		fmt.Fprintln(stdout, "The SafeShell daemon was stopped.")
		return 0
	}
	if len(args) != 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if path == "" {
		fmt.Fprintln(stderr, "safeshell daemon: no private runtime folder was found (XDG_RUNTIME_DIR is not set), so the daemon cannot start. Commands are still checked directly, just a little slower.")
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	l, err := ipc.Listen(path)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell daemon:", err)
		return 1
	}
	defer os.Remove(path)
	srv := &ipc.Server{Handlers: daemonHandlers(stop)}
	fmt.Fprintf(stdout, "SafeShell daemon listening on %s\n", path)
	if err := srv.Serve(ctx, l); err != nil {
		fmt.Fprintln(stderr, "safeshell daemon:", err)
		return 1
	}
	return 0
}

func daemonHandlers(shutdown func()) map[string]ipc.Handler {
	return map[string]ipc.Handler{
		"ping": func(context.Context, json.RawMessage) (any, error) {
			return map[string]any{"protocol": ipc.Protocol, "pid": os.Getpid()}, nil
		},
		"status": func(context.Context, json.RawMessage) (any, error) {
			b, _ := backend()
			ai := "off"
			if b != nil {
				ai = b.Name()
			}
			return map[string]any{"protocol": ipc.Protocol, "ai": ai, "sandbox": probe().CanSimulate()}, nil
		},
		"check": func(_ context.Context, p json.RawMessage) (any, error) {
			var a analyzeParams
			if err := json.Unmarshal(p, &a); err != nil || a.Line == "" {
				return nil, &ipc.Error{Code: ipc.CodeBadParams, Message: "need {line}"}
			}
			return classify.Classify(a.Line), nil
		},
		"analyze": func(ctx context.Context, p json.RawMessage) (any, error) {
			var a analyzeParams
			if err := json.Unmarshal(p, &a); err != nil || a.Line == "" {
				return nil, &ipc.Error{Code: ipc.CodeBadParams, Message: "need {line, cwd}"}
			}
			return analyze(engine.WithEnv(ctx, a.Env), a.Line, a.Cwd), nil
		},
		"shutdown": func(context.Context, json.RawMessage) (any, error) {
			go func() { time.Sleep(50 * time.Millisecond); shutdown() }() // after this reply is sent
			return map[string]bool{"ok": true}, nil
		},
	}
}

// socketPath is replaced in tests.
var socketPath = ipc.SocketPath

// daemonEnv is the part of the caller's environment the analysis needs: the
// variables the simulated command and the tools it runs read. Anything that
// looks like a credential is never included.
func daemonEnv(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if envAllowed[name] || strings.HasPrefix(name, "LC_") {
			up := strings.ToUpper(name)
			if !strings.Contains(up, "KEY") && !strings.Contains(up, "TOKEN") && !strings.Contains(up, "SECRET") && !strings.Contains(up, "PASSWORD") {
				out = append(out, kv)
			}
		}
	}
	return out
}

var envAllowed = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"PWD": true, "OLDPWD": true, "LANG": true, "LANGUAGE": true, "TERM": true, "TMPDIR": true,
	"VIRTUAL_ENV": true, "CONDA_PREFIX": true, "KUBECONFIG": true, "DOCKER_HOST": true,
	"GIT_DIR": true, "GIT_WORK_TREE": true, "GOPATH": true, "GOROOT": true, "NODE_PATH": true,
	"CARGO_HOME": true, "RUSTUP_HOME": true,
	"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true, "XDG_CACHE_HOME": true, "XDG_STATE_HOME": true,
}

// analyzeFast asks the daemon when one is running and falls back to doing the
// work in this process, so the hook works with or without it. A socket that
// does not belong to the user is never used; a short warning says so.
func analyzeFast(ctx context.Context, line, cwd string, stderr io.Writer) engine.Analysis {
	if os.Getenv("SAFESHELL_NODAEMON") == "" {
		var a engine.Analysis
		c, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		err := ipc.Call(c, socketPath(), "analyze", analyzeParams{line, cwd, daemonEnv(os.Environ())}, &a)
		if err == nil {
			return a
		}
		if errors.Is(err, ipc.ErrUntrusted) {
			fmt.Fprintln(stderr, "Warning: the SafeShell daemon socket does not belong to you, so it was ignored. Checking directly instead.")
		}
	}
	return analyze(ctx, line, cwd)
}
