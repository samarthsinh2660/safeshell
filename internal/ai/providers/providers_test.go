package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
)

func TestProvidersAreCompleteAndFindable(t *testing.T) {
	for _, p := range All {
		if p.ID == "" || p.Title == "" || p.DefaultModel == "" || len(p.Env) == 0 || p.KeyURL == "" {
			t.Errorf("incomplete: %+v", p)
		}
		got, ok := Find(p.ID)
		if !ok || got.ID != p.ID {
			t.Errorf("Find(%s)", p.ID)
		}
		b := p.Backend("k", "")
		if b == nil || b.Name() == "" {
			t.Errorf("%s has no backend", p.ID)
		}
	}
	if _, ok := Find("nope"); ok {
		t.Error("invented provider")
	}
}

func TestBackendNamesCarryTheModel(t *testing.T) {
	g, _ := Find("gemini")
	if n := g.Backend("k", "gemini-2.5-pro").Name(); n != "gemini:gemini-2.5-pro" {
		t.Error(n)
	}
	o, _ := Find("openai")
	if n := o.Backend("k", "").Name(); n != "gpt:gpt-6-luna" {
		t.Error(n)
	}
}

func TestKeyFromEnvChecksEachVariable(t *testing.T) {
	g, _ := Find("gemini")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", " second ")
	if g.KeyFromEnv() != "second" {
		t.Error("second variable")
	}
	t.Setenv("GEMINI_API_KEY", "first")
	if g.KeyFromEnv() != "first" {
		t.Error("first variable wins")
	}
}

func listFrom(t *testing.T, id, body string, code int) ([]ai.ModelInfo, error) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	p, _ := Find(id)
	p.URL = srv.URL
	return p.ListModels(context.Background(), "k")
}

func ids(l []ai.ModelInfo) string {
	var s []string
	for _, m := range l {
		s = append(s, m.ID)
	}
	return strings.Join(s, ",")
}

func TestListModelsKeepsAnthropicOrder(t *testing.T) {
	got, err := listFrom(t, "anthropic", `{"data":[{"id":"claude-b"},{"id":"claude-a"}]}`, 200)
	if err != nil || ids(got) != "claude-b,claude-a" {
		t.Errorf("%v %v", got, err)
	}
}

func TestListModelsDropsOpenAIModelsThatCannotChat(t *testing.T) {
	got, err := listFrom(t, "openai", `{"data":[{"id":"text-embedding-3"},{"id":"gpt-z"},{"id":"tts-1"},{"id":"whisper-1"},{"id":"dall-e-3"},{"id":"omni-moderation"},{"id":"gpt-a"},{"id":"gpt-image-2"}]}`, 200)
	if err != nil || ids(got) != "gpt-a,gpt-z" {
		t.Errorf("%v %v", got, err)
	}
}

func TestListModelsKeepsOnlyGeminiChatModels(t *testing.T) {
	got, err := listFrom(t, "gemini", `{"data":[{"id":"models/gemini-b"},{"id":"models/text-embedding-004"},{"id":"models/imagen-3"},{"id":"models/gemini-a"},{"id":"models/gemini-embedding-001"}]}`, 200)
	if err != nil || ids(got) != "gemini-a,gemini-b" {
		t.Errorf("%v %v", got, err)
	}
}

func TestListModelsReportsFailures(t *testing.T) {
	if _, err := listFrom(t, "openai", "secret", 401); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("%v", err)
	}
}
