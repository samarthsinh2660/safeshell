package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

type menuFx struct {
	m         *modelMenu
	out       *strings.Builder
	installed []string
	checked   []string
}

func newMenu(t *testing.T, input string, key string, checkErr error) *menuFx {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, e := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY", "SAFESHELL_AI"} {
		t.Setenv(e, "")
	}
	fx := &menuFx{out: &strings.Builder{}}
	fx.m = newModelMenu(strings.NewReader(input), fx.out)
	fx.m.cfgPath = filepath.Join(t.TempDir(), "config.json")
	fx.m.dirs = runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}
	fx.m.install = func(_ context.Context, _ config.Config, md models.Model, _ io.Writer) error {
		fx.installed = append(fx.installed, md.ID)
		return nil
	}
	fx.m.check = func(p providers.Provider, k, model string) error {
		fx.checked = append(fx.checked, p.ID+":"+k)
		return checkErr
	}
	fx.m.secret = func(string) (string, error) { return key, nil }
	return fx
}

func (fx *menuFx) cfg(t *testing.T) config.Config {
	c, err := config.Load(fx.m.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMenuListsLocalAndCloudChoices(t *testing.T) {
	fx := newMenu(t, "q\n", "", nil)
	fx.m.Run(context.Background())
	for _, want := range []string{"Qwen2.5-Coder 3B", "not downloaded", "Claude", "GPT", "Gemini", "no key yet", "On this machine", "In the cloud"} {
		if !strings.Contains(fx.out.String(), want) {
			t.Errorf("menu lacks %q:\n%s", want, fx.out)
		}
	}
}

func TestPickingALocalModelDownloadsItThenUsesIt(t *testing.T) {
	fx := newMenu(t, "4\n\nq\n", "", nil) // 4 = the 7B model; Enter accepts the download
	fx.m.Run(context.Background())
	if len(fx.installed) != 1 || fx.installed[0] != "qwen2.5-coder-7b" {
		t.Errorf("installed %v", fx.installed)
	}
	if c := fx.cfg(t); c.AI.Mode != ai.Local || c.AI.Local.Model != "qwen2.5-coder-7b" {
		t.Errorf("%+v", c.AI)
	}
}

func TestDecliningTheDownloadChangesNothing(t *testing.T) {
	fx := newMenu(t, "2\nn\nq\n", "", nil)
	fx.m.Run(context.Background())
	if len(fx.installed) != 0 || fx.cfg(t).AI.Mode != ai.Auto {
		t.Errorf("installed %v mode %s", fx.installed, fx.cfg(t).AI.Mode)
	}
}

func TestAFailedDownloadIsReportedAndNotActivated(t *testing.T) {
	fx := newMenu(t, "1\n\nq\n", "", nil)
	fx.m.install = func(context.Context, config.Config, models.Model, io.Writer) error { return errors.New("disk full") }
	fx.m.Run(context.Background())
	if !strings.Contains(fx.out.String(), "Download failed: disk full") || fx.cfg(t).AI.Mode != ai.Auto {
		t.Errorf("%s", fx.out)
	}
}

func TestConnectingAProviderChecksAndStoresTheKey(t *testing.T) {
	fx := newMenu(t, "7\nq\n", "AIza-test-key-123", nil) // 7 = Gemini
	fx.m.Run(context.Background())
	g, _ := providers.Find("gemini")
	if config.Key(g) != "AIza-test-key-123" || len(fx.checked) != 1 {
		t.Errorf("key %q checked %v", config.Key(g), fx.checked)
	}
	if c := fx.cfg(t); c.AI.Mode != ai.Cloud || c.AI.Cloud.Provider != "gemini" {
		t.Errorf("%+v", c.AI)
	}
}

func TestABadKeyIsNeverSaved(t *testing.T) {
	fx := newMenu(t, "6\nq\n", "sk-bad-key-1234567", errors.New("gpt answered 401"))
	fx.m.Run(context.Background())
	o, _ := providers.Find("openai")
	if config.Key(o) != "" || fx.cfg(t).AI.Mode != ai.Auto || !strings.Contains(fx.out.String(), "401") {
		t.Errorf("key %q mode %s\n%s", config.Key(o), fx.cfg(t).AI.Mode, fx.out)
	}
	fx = newMenu(t, "6\nq\n", "short", nil)
	fx.m.Run(context.Background())
	if len(fx.checked) != 0 || !strings.Contains(fx.out.String(), "does not look like an API key") {
		t.Error("a malformed key must be rejected before any request")
	}
}

func TestExistingKeyActivatesWithoutAskingAgainAndKeyCommandReplaces(t *testing.T) {
	fx := newMenu(t, "5\nkey 5\nq\n", "sk-ant-new-key-1234", nil)
	a, _ := providers.Find("anthropic")
	config.SaveKey(a, "sk-ant-old-key-1234")
	fx.m.Run(context.Background())
	if len(fx.checked) != 1 || fx.checked[0] != "anthropic:sk-ant-new-key-1234" || config.Key(a) != "sk-ant-new-key-1234" {
		t.Errorf("checked %v key %q", fx.checked, config.Key(a))
	}
}

func TestModeAndCloudModelCommands(t *testing.T) {
	fx := newMenu(t, "model 6 gpt-4.1\nhybrid\noff\nbogus\n99\nmodel 2 x\nq\n", "", nil)
	fx.m.Run(context.Background())
	c := fx.cfg(t)
	if c.AI.Cloud.Provider != "openai" || c.AI.Cloud.Model != "gpt-4.1" || c.AI.Mode != ai.Off {
		t.Errorf("%+v", c.AI)
	}
	if strings.Count(fx.out.String(), "Please type one of the numbers") != 2 || !strings.Contains(fx.out.String(), "needs a cloud model number") {
		t.Errorf("%s", fx.out)
	}
}

func TestSlashModelsAndHelpDoNotRunAnything(t *testing.T) {
	answer(t, "q\n")
	code, out, errOut := runSS("guard", "--unknown", "--", "/models")
	if code != 1 || out != "" || !strings.Contains(errOut, "SafeShell AI models") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
	code, out, errOut = runSS("guard", "--unknown", "--", "/help models")
	if code != 1 || out != "" || !strings.Contains(errOut, "Claude") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestHelpTopicsAreCompleteAndUnknownOnesAreExplained(t *testing.T) {
	code, out, _ := runSS("help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, tp := range helpTopics {
		if !strings.Contains(out, tp.name) || tp.body == "" {
			t.Errorf("topic %s missing from the overview", tp.name)
		}
		if c, o, _ := runSS("help", tp.name); c != 0 || !strings.Contains(o, tp.title) {
			t.Errorf("topic %s: %d", tp.name, c)
		}
	}
	if c, o, e := runSS("help", "nonsense"); c != 2 || o != "" || !strings.Contains(e, "No help topic") || !strings.Contains(e, "start") {
		t.Errorf("%d %q %q", c, o, e)
	}
	if c, o, _ := runSS("--help"); c != 0 || !strings.Contains(o, "safeshell help start") {
		t.Errorf("%d %q", c, o)
	}
}

func TestHelpMentionsEveryCommandTheProgramHas(t *testing.T) {
	_, out, _ := runSS("help", "commands")
	for _, c := range []string{"check", "analyze", "simulate", "explain", "ask", "undo", "snapshots", "models", "ai setup", "hook", "doctor", "/models", "/help"} {
		if !strings.Contains(out, c) {
			t.Errorf("commands topic lacks %s", c)
		}
	}
}

func TestBuildBackendByMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, e := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GOOGLE_API_KEY"} {
		t.Setenv(e, "")
	}
	d := runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}
	load := func(mode string, cloud string) config.Config {
		c, _ := config.Load(filepath.Join(t.TempDir(), "none"))
		c.AI.Mode = ai.Mode(mode)
		c.AI.Cloud.Provider = cloud
		return c
	}
	if b, err := buildBackend(load("auto", ""), d); b != nil || err != nil {
		t.Error("auto with nothing downloaded must be off")
	}
	if b, _ := buildBackend(load("off", ""), d); b != nil {
		t.Error("off")
	}
	if b, _ := buildBackend(load("local", ""), d); b == nil || b.Name() != "local:qwen2.5-coder-3b" {
		t.Errorf("local: %v", b)
	}
	if _, err := buildBackend(load("cloud", ""), d); err == nil || !strings.Contains(err.Error(), "/models") {
		t.Errorf("cloud without provider: %v", err)
	}
	if _, err := buildBackend(load("cloud", "openai"), d); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("cloud without key: %v", err)
	}
	p, _ := providers.Find("openai")
	config.SaveKey(p, "sk-test-key-123456")
	if b, err := buildBackend(load("cloud", "openai"), d); err != nil || b.Name() != "gpt:gpt-6-luna" {
		t.Errorf("cloud: %v %v", b, err)
	}
	if b, _ := buildBackend(load("hybrid", "openai"), d); b == nil || b.Name() != "local:qwen2.5-coder-3b+gpt:gpt-6-luna" {
		t.Errorf("hybrid: %v", b)
	}
	if b, _ := buildBackend(load("hybrid", ""), d); b == nil || b.Name() != "local:qwen2.5-coder-3b" {
		t.Errorf("hybrid without cloud still works locally: %v", b)
	}
}

func TestAIStatusStopAndSetupUsage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if code, out, _ := runSS("ai", "stop"); code != 0 || !strings.Contains(out, "not running") {
		t.Errorf("%d %q", code, out)
	}
	if code, _, _ := runSS("ai", "bogus"); code != 2 {
		t.Errorf("%d", code)
	}
}

func TestBenchNeedsAnActiveModelAndRejectsUnknownOnes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SAFESHELL_AI", "off")
	if code, _, e := runSS("ai", "bench"); code != 1 || !strings.Contains(e, "/models") {
		t.Errorf("%d %q", code, e)
	}
	if code, _, _ := runSS("ai", "bench", "--model", "gpt-9"); code != 2 {
		t.Errorf("%d", code)
	}
	if code, _, _ := runSS("ai", "bench", "extra"); code != 2 {
		t.Errorf("%d", code)
	}
}

func TestAnEnvironmentOverrideIsNeverSavedByTheMenu(t *testing.T) {
	fx := newMenu(t, "model 6 gpt-4.1\nq\n", "", nil)
	t.Setenv("SAFESHELL_AI", "off")
	fx.m.Run(context.Background())
	if m := fx.cfg(t).AI.Mode; m != ai.Auto {
		t.Errorf("the temporary SAFESHELL_AI=off was written to the config as %q", m)
	}
}

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
