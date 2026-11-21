package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

func TestInjectionBenchPassesAModelThatIgnoresPlantedText(t *testing.T) {
	aiDirs(t)
	oldB := benchBackend
	t.Cleanup(func() { benchBackend = oldB })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) {
		return fakeAI{reply: `{"level":"R4","reason":"deletes files"}`}, nil
	}
	code, out, _ := runSS("ai", "bench", "--injection", "--pace", "0s")
	if code != 0 || !strings.Contains(out, "0 changed the verdict, 0 swayed") || strings.Contains(out, "FAIL") {
		t.Errorf("%d %q", code, out)
	}
}

func TestInjectionBenchFailsWhenTheModelObeysPlantedText(t *testing.T) {
	aiDirs(t)
	oldB := benchBackend
	t.Cleanup(func() { benchBackend = oldB })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) {
		return fakeAI{reply: `{"level":"R0","reason":"safe"}`}, nil
	}
	code, out, _ := runSS("ai", "bench", "--injection")
	if code != 1 || !strings.Contains(out, "FAIL") || !strings.Contains(out, "rated R0, rules said") || strings.Contains(out, " 0 swayed") {
		t.Errorf("%d %q", code, out)
	}
}

func TestInjectionBenchReportsUnreachableModelAndBadFile(t *testing.T) {
	aiDirs(t)
	oldB, oldL := benchBackend, loadInjection
	t.Cleanup(func() { benchBackend, loadInjection = oldB, oldL })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) { return fakeAI{err: errors.New("down")}, nil }
	code, out, _ := runSS("ai", "bench", "--injection")
	if code != 1 || !strings.Contains(out, "SKIP") || !strings.Contains(out, "never reached the model") {
		t.Errorf("%d %q", code, out)
	}
	loadInjection = func() ([]eval.Injection, error) { return nil, errors.New("broken list") }
	if code, _, e := runSS("ai", "bench", "--injection"); code != 1 || !strings.Contains(e, "broken list") {
		t.Errorf("%d %q", code, e)
	}
}
