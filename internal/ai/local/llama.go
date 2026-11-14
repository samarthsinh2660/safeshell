// Package local runs the model on this machine through the managed engine.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	goruntime "runtime"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai/openai"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// Llama is a catalog model served by the managed llama-server.
type Llama struct {
	Dirs  runtime.Dirs
	Model models.Model
	GPU   string    // auto, cuda, vulkan, cpu
	Log   io.Writer // where "starting the model" notices go; may be nil
}

func (l Llama) Name() string { return "local:" + l.Model.ID }

// Ready reports whether an engine and the model file are installed.
func (l Llama) Ready() bool {
	return l.Dirs.ModelInstalled(l.Model) && len(l.Dirs.Installed(goruntime.GOARCH, l.GPU)) > 0
}

// Complete asks the model. Engines are tried best first (CUDA, Vulkan, CPU):
// if the GPU one will not start, the next is used, so a driver problem slows
// the answer down instead of breaking it.
func (l Llama) Complete(ctx context.Context, system, user string) (string, error) {
	if !l.Ready() {
		return "", errors.New("the local model is not downloaded yet: type /models or run  safeshell ai setup")
	}
	var failures []error
	for _, b := range l.Dirs.Installed(goruntime.GOARCH, l.GPU) {
		ep, err := l.Dirs.Ensure(ctx, runtime.Spec{Bin: l.Dirs.ServerBin(b), Model: l.Dirs.ModelPath(l.Model.File), GPU: b.GPU()}, l.Log)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s engine: %w", b.Variant, err))
			continue
		}
		zero := 0.0
		return openai.Client{
			Label: l.Name(), URL: fmt.Sprintf("http://127.0.0.1:%d/v1", ep.Port), Key: ep.Key, Model: l.Model.ID, Temperature: &zero,
			Client: &http.Client{Timeout: 120 * time.Second},
		}.Complete(ctx, system, user)
	}
	return "", errors.Join(failures...)
}
