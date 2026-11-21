package main

import (
	"errors"
	"os"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/local"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// backend returns the configured AI, or nil when it is off. Tests replace it.
var backend = func() (ai.Backend, error) {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		return nil, err
	}
	return buildBackend(c, runtime.DefaultDirs())
}

// localLlama is the local engine as the config describes it; every place that
// asks whether local AI is ready uses it, so they agree with the backend.
func localLlama(c config.Config, dirs runtime.Dirs) local.Llama {
	m, _ := models.Find(c.AI.Local.Model)
	return local.Llama{Dirs: dirs, Model: m, GPU: c.AI.Local.GPU, System: c.AI.Local.Engine == "system"}
}

func buildBackend(c config.Config, dirs runtime.Dirs) (ai.Backend, error) {
	loc := localLlama(c, dirs)
	loc.Log = os.Stderr
	cloud := func() (ai.Backend, error) {
		p, ok := providers.Find(c.AI.Cloud.Provider)
		if !ok {
			return nil, errors.New("no cloud provider chosen: type /models")
		}
		key, err := config.Key(p)
		if err != nil {
			return nil, err
		}
		if key == "" {
			return nil, errors.New("no API key for " + p.Title + ": type /models")
		}
		return p.Backend(key, c.AI.Cloud.Model), nil
	}
	switch c.AI.Mode {
	case ai.Local:
		return loc, nil
	case ai.Cloud:
		return cloud()
	case ai.Hybrid:
		chain := ai.Chain{loc}
		if cb, err := cloud(); err == nil {
			chain = append(chain, cb)
		}
		return chain, nil
	case ai.Auto:
		if loc.Ready() {
			return loc, nil
		}
	}
	return nil, nil
}
