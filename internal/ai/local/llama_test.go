package local

import (
	"context"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

var tiny = models.Model{ID: "tiny", File: "tiny.gguf", Size: 4}

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
}
