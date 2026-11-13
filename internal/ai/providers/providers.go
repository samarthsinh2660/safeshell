// Package providers lists the hosted models SafeShell can connect to.
package providers

import (
	"context"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/cloud"
	"github.com/samarthsinh2660/safeshell/internal/ai/openai"
)

// Provider is a hosted AI service reached with an API key.
type Provider struct {
	ID           string
	Title        string // what the person recognises: Claude, GPT, Gemini
	Maker        string
	Env          []string // environment variables that hold the key, in order
	DefaultModel string
	KeyURL       string // where to create a key
	URL          string // API base address
}

// All is every provider on offer.
var All = []Provider{
	{ID: "anthropic", Title: "Claude", Maker: "Anthropic", Env: []string{"ANTHROPIC_API_KEY"}, DefaultModel: "claude-haiku-5-5", KeyURL: "https://console.anthropic.com/settings/keys", URL: "https://api.anthropic.com"},
	{ID: "openai", Title: "GPT", Maker: "OpenAI", Env: []string{"OPENAI_API_KEY"}, DefaultModel: "gpt-6-luna", KeyURL: "https://platform.openai.com/api-keys", URL: "https://api.openai.com/v1"},
	{ID: "gemini", Title: "Gemini", Maker: "Google", Env: []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"}, DefaultModel: "gemini-3.5-flash-lite", KeyURL: "https://aistudio.google.com/apikey", URL: "https://generativelanguage.googleapis.com/v1beta/openai"},
}

// Find returns the provider with the given id.
func Find(id string) (Provider, bool) {
	for _, p := range All {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// KeyFromEnv returns the key set in the environment, if any.
func (p Provider) KeyFromEnv() string {
	for _, e := range p.Env {
		if v := strings.TrimSpace(os.Getenv(e)); v != "" {
			return v
		}
	}
	return ""
}

// Backend builds the client. model "" means the provider's default. Everything
// sent passes through ai.Redact first.
func (p Provider) Backend(key, model string) ai.Backend {
	if model == "" {
		model = p.DefaultModel
	}
	switch p.ID {
	case "anthropic":
		return ai.Redacting(cloud.Anthropic{Key: key, Model: model, URL: p.URL})
	case "openai":
		return ai.Redacting(openai.Client{Label: "gpt:" + model, URL: p.URL, Key: key, Model: model, Tokens: "max_completion_tokens", Reasoning: "low"})
	default:
		return ai.Redacting(openai.Client{Label: "gemini:" + model, URL: p.URL, Key: key, Model: model})
	}
}

// notChat marks ids of models that cannot answer a text request.
var notChat = []string{"embedding", "tts", "whisper", "dall-e", "moderation", "image", "audio", "realtime", "transcribe"}

// ListModels asks the provider which models this key can use, keeping only
// those that can chat. It gives up after 10 seconds.
func (p Provider) ListModels(ctx context.Context, key string) ([]ai.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if p.ID == "anthropic" {
		return cloud.Anthropic{Key: key, URL: p.URL}.ListModels(ctx)
	}
	all, err := openai.Client{Label: p.ID, URL: p.URL, Key: key}.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	var out []ai.ModelInfo
	for _, m := range all {
		m.ID = strings.TrimPrefix(m.ID, "models/") // Gemini prefixes its ids
		if p.ID == "gemini" && !strings.Contains(m.ID, "gemini") {
			continue
		}
		if !slices.ContainsFunc(notChat, func(w string) bool { return strings.Contains(m.ID, w) }) {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(a, b ai.ModelInfo) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

// Check makes one tiny request to prove the key and model work.
func Check(b ai.Backend) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := b.Complete(ctx, "Reply with the single word: ok", "ping")
	return err
}
