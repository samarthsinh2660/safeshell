// Package cloud talks to the Anthropic Messages API.
package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai"
)

// Anthropic is a hosted model. The caller should wrap it with ai.Redacting.
type Anthropic struct {
	Key    string
	Model  string // default claude-haiku-5-5
	URL    string // default https://api.anthropic.com
	Client *http.Client
}

func (a Anthropic) Name() string { return "cloud:" + a.model() }

func (a Anthropic) model() string {
	if a.Model == "" {
		return "claude-haiku-5-5"
	}
	return a.Model
}

// ListModels returns the models this key can use, newest first.
func (a Anthropic) ListModels(ctx context.Context) ([]ai.ModelInfo, error) {
	if a.Key == "" {
		return nil, errors.New("no API key (set ANTHROPIC_API_KEY)")
	}
	return ai.FetchModels(ctx, a.Client, "cloud:anthropic", strings.TrimRight(a.URL, "/")+"/v1/models?limit=100",
		map[string]string{"x-api-key": a.Key, "anthropic-version": "2023-06-01"})
}

func (a Anthropic) Complete(ctx context.Context, system, user string) (string, error) {
	if a.Key == "" {
		return "", errors.New("no API key (set ANTHROPIC_API_KEY)")
	}
	base := strings.TrimRight(a.URL, "/")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	body, _ := json.Marshal(map[string]any{
		"model": a.model(), "max_tokens": 1500, "temperature": 0, "system": system,
		"messages": []map[string]string{{"role": "user", "content": user}},
	})
	c := a.Client
	if c == nil {
		c = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := ai.Do(ctx, c, func() (*http.Request, error) {
		req, err := http.NewRequest(http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", a.Key)
		req.Header.Set("anthropic-version", "2023-06-01")
		return req, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", ai.StatusError(a.Name(), resp.StatusCode) // the body can echo the prompt, so it is not shown
	}
	var out struct {
		Content []struct{ Type, Text string } `json:"content"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", errors.New("unreadable reply")
	}
	var sb strings.Builder
	for _, c := range out.Content {
		if c.Type == "text" {
			sb.WriteString(c.Text)
		}
	}
	if sb.Len() == 0 {
		return "", errors.New("empty reply")
	}
	return sb.String(), nil
}
