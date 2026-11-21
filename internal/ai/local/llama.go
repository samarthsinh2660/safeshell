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

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/openai"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// Llama is a catalog model served by the managed llama-server.
type Llama struct {
	Dirs  runtime.Dirs
	Model models.Model
	GPU   string // auto, cuda, vulkan, cpu
	// System uses the llama-server found on PATH instead of the downloaded build (ai.local.engine "system").
	System bool
	Log    io.Writer // where "starting the model" notices go; may be nil
}

func (l Llama) Name() string { return "local:" + l.Model.ID }

// engine is one way to start the server: a label for error messages and what to run.
type engine struct {
	label string
	spec  runtime.Spec
}

// engines lists what can serve the model, best first. In system mode that is the llama-server on PATH, or an
// error naming what to install.
func (l Llama) engines() ([]engine, error) {
	model := l.Dirs.ModelPath(l.Model.File)
	if l.System {
		bin, err := runtime.SystemServer()
		if err != nil {
			return nil, err
		}
		gpu := l.GPU != "cpu" && l.GPU != "off"
		return []engine{{"system", runtime.Spec{Bin: bin, Model: model, GPU: gpu, System: true}}}, nil
	}
	var out []engine
	for _, b := range l.Dirs.Installed(goruntime.GOARCH, l.GPU) {
		out = append(out, engine{b.Variant, runtime.Spec{Bin: l.Dirs.ServerBin(b), Model: model, GPU: b.GPU()}})
	}
	return out, nil
}

// Ready reports whether an engine and the model file are installed.
func (l Llama) Ready() bool {
	engines, err := l.engines()
	return err == nil && len(engines) > 0 && l.Dirs.ModelInstalled(l.Model)
}

// Complete asks the model. Engines are tried best first (CUDA, Vulkan, CPU):
// if the GPU one will not start, the next is used, so a driver problem slows
// the answer down instead of breaking it.
func (l Llama) Complete(ctx context.Context, system, user string) (string, error) {
	engines, err := l.engines()
	if err != nil {
		return "", fmt.Errorf("%w: %w", ai.ErrUnavailable, err)
	}
	if len(engines) == 0 || !l.Dirs.ModelInstalled(l.Model) {
		return "", fmt.Errorf("%w: the local model is not downloaded yet: type /models or run  safeshell ai setup", ai.ErrUnavailable)
	}
	var failures []error
	for _, e := range engines {
		ep, err := l.Dirs.Ensure(ctx, e.spec, l.Log)
		if err != nil {
			if ctx.Err() == nil { // out of time is not the engine's fault
				err = fmt.Errorf("%w: %w", ai.ErrUnavailable, err)
			}
			failures = append(failures, fmt.Errorf("%s engine: %w", e.label, err))
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
