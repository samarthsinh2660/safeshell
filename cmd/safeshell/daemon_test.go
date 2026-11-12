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

	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
)

func TestDaemonEnvKeepsOnlyWhatTheAnalysisNeeds(t *testing.T) {
	got := daemonEnv([]string{
		"PATH=/usr/bin", "HOME=/home/a", "VIRTUAL_ENV=/v", "KUBECONFIG=/k", "LC_ALL=C",
		"OPENAI_API_KEY=sk-1", "GITHUB_TOKEN=t", "DB_PASSWORD=p", "AWS_SECRET_ACCESS_KEY=s",
		"LC_SECRET=x", "RANDOM_OTHER=1", "NOEQUALS",
	})
	want := []string{"PATH=/usr/bin", "HOME=/home/a", "VIRTUAL_ENV=/v", "KUBECONFIG=/k", "LC_ALL=C"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestAnalyzeFastSendsNoSecretsToTheDaemon(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	t.Setenv("OPENAI_API_KEY", "sk-secret")
	t.Setenv("PATH", "/usr/bin")
	old := socketPath
	t.Cleanup(func() { socketPath = old })
	path := filepath.Join(t.TempDir(), "d.sock")
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
	if !slices.Contains(got.Env, "PATH=/usr/bin") || slices.ContainsFunc(got.Env, func(s string) bool { return strings.Contains(s, "sk-secret") }) {
		t.Errorf("forwarded %v", got.Env)
	}
}

func TestAnalyzeFastIgnoresASocketThatIsNotOursAndWarns(t *testing.T) {
	t.Setenv("SAFESHELL_NODAEMON", "")
	local := sampleAnalysis()
	local.Line = "ran-locally"
	fakeAnalyze(t, local)
	fake := filepath.Join(t.TempDir(), "fake.sock")
	if err := os.WriteFile(fake, nil, 0o600); err != nil { // a file, not a socket
		t.Fatal(err)
	}
	socketPath = func() string { return fake }
	var warn strings.Builder
	if a := analyzeFast(context.Background(), "x", "/", &warn); a.Line != "ran-locally" {
		t.Errorf("must analyse directly: %q", a.Line)
	}
	if !strings.Contains(warn.String(), "does not belong to you") {
		t.Errorf("warning: %q", warn.String())
	}
}

func TestDaemonRefusesToStartWithoutARuntimeFolder(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if code, _, e := runSS("daemon"); code != 1 || !strings.Contains(e, "XDG_RUNTIME_DIR") {
		t.Errorf("%d %q", code, e)
	}
}
