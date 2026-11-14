package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai"
)

func TestAnthropicRequestAndReply(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"content":[{"type":"text","text":"{\"level\":"},{"type":"text","text":"\"R3\"}"}]}`))
	}))
	defer srv.Close()
	out, err := Anthropic{Key: "k", URL: srv.URL}.Complete(context.Background(), "sys", "usr")
	if err != nil || out != `{"level":"R3"}` {
		t.Fatalf("%q %v", out, err)
	}
	if hdr.Get("x-api-key") != "k" || hdr.Get("anthropic-version") == "" || got["model"] != "claude-haiku-5-5" || got["system"] != "sys" {
		t.Errorf("hdr %v body %v", hdr, got)
	}
}

func TestAnthropicErrorsDoNotEchoTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "your prompt was: rm -rf secret", 401)
	}))
	defer srv.Close()
	_, err := Anthropic{Key: "k", URL: srv.URL}.Complete(context.Background(), "", "")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "401") {
		t.Errorf("%v", err)
	}
	if _, err := (Anthropic{}).Complete(context.Background(), "", ""); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("missing key: %v", err)
	}
}

func TestAnthropicListsModels(t *testing.T) {
	var hdr http.Header
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr, path = r.Header, r.URL.Path
		w.Write([]byte(`{"data":[{"id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5"}]}`))
	}))
	defer srv.Close()
	got, err := Anthropic{Key: "k", URL: srv.URL + "/"}.ListModels(context.Background())
	if err != nil || len(got) != 1 || got[0].Name != "Claude Haiku 5.5" || path != "/v1/models" {
		t.Fatalf("%v %v %q", got, err, path)
	}
	if hdr.Get("x-api-key") != "k" || hdr.Get("anthropic-version") == "" {
		t.Errorf("hdr %v", hdr)
	}
	if _, err := (Anthropic{}).ListModels(context.Background()); err == nil || !strings.Contains(err.Error(), "API key") {
		t.Errorf("missing key: %v", err)
	}
}

func serveBody(t *testing.T, code int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCompleteRejectsUnusableReplies(t *testing.T) {
	for name, body := range map[string]string{"bad JSON": "{nope", "no content": `{"content":[]}`, "no text blocks": `{"content":[{"type":"tool_use"}]}`} {
		srv := serveBody(t, 200, body)
		if _, err := (Anthropic{Key: "k", URL: srv.URL}).Complete(context.Background(), "", ""); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	srv := serveBody(t, 500, "secret prompt")
	old := ai.RetryDelay
	ai.RetryDelay = time.Millisecond
	defer func() { ai.RetryDelay = old }()
	if _, err := (Anthropic{Key: "k", URL: srv.URL}).Complete(context.Background(), "", ""); err == nil || strings.Contains(err.Error(), "secret") {
		t.Errorf("non-200: %v", err)
	}
}

func TestNamesAndDefaults(t *testing.T) {
	if got := (Anthropic{}).Name(); got != "cloud:claude-haiku-5-5" {
		t.Error(got)
	}
	if got := (Anthropic{Model: "claude-x"}).Name(); got != "cloud:claude-x" {
		t.Error(got)
	}
	if got := (Anthropic{}).base(); got != "https://api.anthropic.com" {
		t.Error(got)
	}
	if got := (Anthropic{URL: "http://h:1/"}).base(); got != "http://h:1" {
		t.Error(got)
	}
	if got := (Anthropic{URL: "/"}).base(); got != "https://api.anthropic.com" {
		t.Error("a bare slash is no address:", got)
	}
}
