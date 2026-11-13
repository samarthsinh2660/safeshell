// Package openai speaks the OpenAI-style chat API. The same client reaches
// OpenAI (GPT), Google Gemini's compatible endpoint, and the local llama-server.
package openai

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

// Client is one chat endpoint.
type Client struct {
	Label  string // shown in reports, e.g. "gpt:gpt-6-luna"
	URL    string // base, e.g. https://api.openai.com/v1
	Key    string // empty for the local engine
	Model  string
	Tokens string // name of the length parameter: "max_tokens" (default) or "max_completion_tokens"
	// Temperature is sent only when set; some hosted models accept just their default.
	Temperature *float64
	Client      *http.Client
}

func (c Client) Name() string { return c.Label }

func (c Client) Complete(ctx context.Context, system, user string) (string, error) {
	tokens := c.Tokens
	if tokens == "" {
		tokens = "max_tokens"
	}
	req := map[string]any{
		"model": c.Model, tokens: 1500, // room for a thinking model to think and still finish its answer
		"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
	}
	if c.Temperature != nil {
		req["temperature"] = *c.Temperature
	}
	body, _ := json.Marshal(req)
	cl := c.Client
	if cl == nil {
		cl = &http.Client{Timeout: 90 * time.Second}
	}
	resp, err := ai.Do(ctx, cl, func() (*http.Request, error) {
		hr, err := http.NewRequest(http.MethodPost, strings.TrimRight(c.URL, "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		hr.Header.Set("Content-Type", "application/json")
		if c.Key != "" {
			hr.Header.Set("Authorization", "Bearer "+c.Key)
		}
		return hr, nil
	})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", ai.StatusError(c.Label, resp.StatusCode) // the body can echo the prompt, so it is not shown
	}
	var out struct {
		Choices []struct{ Message struct{ Content string } }
	}
	if json.Unmarshal(b, &out) != nil || len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", errors.New(c.Label + " sent no usable reply")
	}
	return out.Choices[0].Message.Content, nil
}
