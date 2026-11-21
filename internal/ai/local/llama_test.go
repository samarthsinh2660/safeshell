package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

var tiny = models.Model{ID: "tiny", File: "tiny.gguf", Size: 4, SHA: "03ac674216f3e15c761ee1a5e255f067953623c8b388b4459e13f978d7c846f4"} // SHA-256 of "1234"

func TestNameCarriesTheModel(t *testing.T) {
	if got := (Llama{Model: tiny}).Name(); got != "local:tiny" {
		t.Error(got)
	}
}

func TestNotReadyUntilTheModelAndAnEngineAreInstalled(t *testing.T) {
	dirs := runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}
	l := Llama{Dirs: dirs, Model: tiny, GPU: "cpu"}
	if l.Ready() {
		t.Fatal("nothing is installed yet")
	}
	_, err := l.Complete(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "safeshell ai setup") {
		t.Errorf("the error should say what to run: %v", err)
	}
	if !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("a missing model must be reported as unavailable: %v", err)
	}

	if err := os.MkdirAll(filepath.Dir(dirs.ModelPath(tiny.File)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ModelPath(tiny.File), []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	if l.Ready() {
		t.Error("the model alone is not enough")
	}

	cands := runtime.Candidates(goruntime.GOARCH, "cpu")
	if len(cands) == 0 {
		t.Skip("no engine build for this architecture")
	}
	bin := dirs.ServerBin(cands[0])
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !l.Ready() {
		t.Error("model and engine are both installed")
	}
}

func TestCompleteReportsEnginesThatWillNotStart(t *testing.T) {
	dirs := runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}
	l := Llama{Dirs: dirs, Model: tiny, GPU: "cpu"}
	if err := os.MkdirAll(filepath.Dir(dirs.ModelPath(tiny.File)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ModelPath(tiny.File), []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	cands := runtime.Candidates(goruntime.GOARCH, "cpu")
	if len(cands) == 0 {
		t.Skip("no engine build for this architecture")
	}
	bin := dirs.ServerBin(cands[0])
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil { // exits at once
		t.Fatal(err)
	}
	_, err := l.Complete(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "cpu engine") {
		t.Errorf("%v", err)
	}
	if !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("an engine that will not start must be reported as unavailable: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Complete(ctx, "", ""); !errors.Is(err, context.Canceled) || errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("running out of time is not the engine being unavailable: %v", err)
	}
}

// systemLlama is a Llama in system mode with the tiny model placed by hand and, when withEngine is set, the fake
// llama-server from the integration tests as the only llama-server on PATH.
func systemLlama(t *testing.T, withEngine bool) Llama {
	t.Helper()
	dirs := runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}
	if err := os.MkdirAll(filepath.Dir(dirs.ModelPath(tiny.File)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dirs.ModelPath(tiny.File), []byte("1234"), 0o600); err != nil { // no marker: checked against the SHA-256
		t.Fatal(err)
	}
	bin := t.TempDir()
	if withEngine {
		build := exec.Command("go", "build", "-o", filepath.Join(bin, "llama-server"), "../../../test/integration/fakellama")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build fake engine: %v\n%s", err, out)
		}
	}
	t.Setenv("PATH", bin)
	t.Cleanup(func() { dirs.Stop() })
	return Llama{Dirs: dirs, Model: tiny, GPU: "auto", System: true}
}

func TestSystemEngineStartsAndAnswersWithNothingDownloaded(t *testing.T) {
	t.Setenv("FAKE_REPLY", "hello from the system engine")
	l := systemLlama(t, true)
	if !l.Ready() {
		t.Fatal("a hand-placed model and llama-server on PATH are enough")
	}
	got, err := l.Complete(context.Background(), "s", "u")
	if err != nil || got != "hello from the system engine" {
		t.Fatalf("%q %v", got, err)
	}
	for _, sub := range []string{"engine", "downloads"} {
		if _, err := os.Stat(filepath.Join(l.Dirs.Data, sub)); err == nil {
			t.Errorf("%s was created: nothing may be downloaded for the engine", sub)
		}
	}
}

func TestSystemEngineMissingNamesWhatToInstall(t *testing.T) {
	l := systemLlama(t, false)
	if l.Ready() {
		t.Error("not ready without llama-server")
	}
	_, err := l.Complete(context.Background(), "", "")
	if err == nil || !strings.Contains(err.Error(), "install llama.cpp") || !errors.Is(err, ai.ErrUnavailable) {
		t.Errorf("%v", err)
	}
}

func TestSystemEngineNeedsTheModel(t *testing.T) {
	l := systemLlama(t, true)
	if err := os.Remove(l.Dirs.ModelPath(tiny.File)); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "safeshell ai setup") {
		t.Errorf("%v", err)
	}
}

func TestSystemEngineChecksAHandPlacedModelAgainstThePinnedHash(t *testing.T) {
	l := systemLlama(t, true)
	if err := os.WriteFile(l.Dirs.ModelPath(tiny.File), []byte("abcd"), 0o600); err != nil { // right size, wrong content
		t.Fatal(err)
	}
	if l.Ready() {
		t.Error("a model with the wrong SHA-256 must not be used")
	}
}

func TestSystemEngineRunsOnTheCPUWhenAskedTo(t *testing.T) {
	l := systemLlama(t, true)
	l.GPU = "cpu"
	engines, err := l.engines()
	if err != nil || len(engines) != 1 || engines[0].spec.GPU || !engines[0].spec.System {
		t.Errorf("%+v %v", engines, err)
	}
}
