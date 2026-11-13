package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
)

func fakeList(list []ai.ModelInfo, err error) func(context.Context, providers.Provider, string) ([]ai.ModelInfo, error) {
	return func(context.Context, providers.Provider, string) ([]ai.ModelInfo, error) { return list, err }
}

var offered = []ai.ModelInfo{{ID: "claude-haiku-5-5", Name: "Claude Haiku 5.5"}, {ID: "claude-opus-5"}}

func TestChoosingAModelFromTheKeyListSavesIt(t *testing.T) {
	fx := newMenu(t, "choose 5\n2\nq\n", "", nil)
	a, _ := providers.Find("anthropic")
	config.SaveKey(a, "sk-ant-old-key-1234")
	fx.m.list = fakeList(offered, nil)
	fx.m.Run(context.Background())
	if c := fx.cfg(t); c.AI.Cloud.Provider != "anthropic" || c.AI.Cloud.Model != "claude-opus-5" {
		t.Errorf("%+v", c.AI.Cloud)
	}
	for _, want := range []string{"1) claude-haiku-5-5  (Claude Haiku 5.5)  <- in use", "2) claude-opus-5\n", "will use the model claude-opus-5"} {
		if !strings.Contains(fx.out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, fx.out)
		}
	}
}

func TestChooseKeepsTheModelWhenNothingValidIsPicked(t *testing.T) {
	fx := newMenu(t, "choose 5\n\nchoose 5\n9\nq\n", "", nil)
	a, _ := providers.Find("anthropic")
	config.SaveKey(a, "sk-ant-old-key-1234")
	fx.m.list = fakeList(offered, nil)
	fx.m.Run(context.Background())
	if strings.Count(fx.out.String(), "No change. The model stays claude-haiku-5-5.") != 2 {
		t.Errorf("%s", fx.out)
	}
	if _, err := config.Load(fx.m.cfgPath); err != nil {
		t.Error(err)
	}
}

func TestChooseNeedsAKeyAndSurvivesAListFailure(t *testing.T) {
	fx := newMenu(t, "choose 5\nchoose 6\nchoose 7\nchoose\nq\n", "", nil)
	o, _ := providers.Find("openai")
	config.SaveKey(o, "sk-openai-key-1234")
	g, _ := providers.Find("gemini")
	config.SaveKey(g, "gemini-key-12345")
	calls := 0
	fx.m.list = func(context.Context, providers.Provider, string) ([]ai.ModelInfo, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("could not reach OpenAI: check your network connection")
		}
		return nil, nil
	}
	fx.m.Run(context.Background())
	out := fx.out.String()
	for _, want := range []string{"There is no Claude key yet", "Could not list the GPT models: could not reach OpenAI", "The model stays gpt-6-luna", "Gemini offered no models"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if c := fx.cfg(t); c.AI.Cloud.Model != "" {
		t.Errorf("model changed: %+v", c.AI.Cloud)
	}
}

func runList(t *testing.T, list func(context.Context, providers.Provider, string) ([]ai.ModelInfo, error)) (int, string, string) {
	var out, errw strings.Builder
	code := runModelsList(context.Background(), &out, &errw, list)
	return code, out.String(), errw.String()
}

func TestModelsListPrintsTheActiveProvidersModels(t *testing.T) {
	newMenu(t, "", "", nil) // isolates the settings folder and clears key variables
	a, _ := providers.Find("anthropic")
	config.SaveKey(a, "sk-ant-old-key-1234")
	var c config.Config
	c.AI.Cloud.Provider, c.AI.Cloud.Model = "anthropic", "claude-opus-5"
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	code, out, _ := runList(t, fakeList(offered, nil))
	if code != 0 || out != "claude-haiku-5-5  (Claude Haiku 5.5)\nclaude-opus-5  <- in use\n" {
		t.Errorf("%d %q", code, out)
	}
	code, _, errw := runList(t, fakeList(nil, errors.New("boom")))
	if code != 1 || !strings.Contains(errw, "could not list the Claude models: boom") {
		t.Errorf("%d %q", code, errw)
	}
}

func TestModelsListExplainsWhatIsMissing(t *testing.T) {
	newMenu(t, "", "", nil)
	code, _, errw := runList(t, fakeList(nil, nil))
	if code != 1 || !strings.Contains(errw, "no cloud provider is set") {
		t.Errorf("%d %q", code, errw)
	}
	var c config.Config
	c.AI.Cloud.Provider = "openai"
	config.Save(config.Path(), c)
	code, _, errw = runList(t, fakeList(nil, nil))
	if code != 1 || !strings.Contains(errw, "no GPT key yet") {
		t.Errorf("%d %q", code, errw)
	}
	t.Setenv("SAFESHELL_AI", "bogus")
	if code, _, _ := runList(t, fakeList(nil, nil)); code != 1 {
		t.Errorf("bad settings should fail, got %d", code)
	}
}

func TestMainRoutesModelsList(t *testing.T) {
	newMenu(t, "", "", nil)
	var out, errw strings.Builder
	if code := run([]string{"models", "list"}, &out, &errw, false); code != 1 || !strings.Contains(errw.String(), "no cloud provider is set") {
		t.Errorf("%d %q", code, errw.String())
	}
}

func TestListCloudAsksTheProvider(t *testing.T) {
	p, _ := providers.Find("anthropic")
	p.URL = "http://127.0.0.1:1" // nothing listens here
	if _, err := listCloud(context.Background(), p, "k"); err == nil {
		t.Error("expected a network error")
	}
}
