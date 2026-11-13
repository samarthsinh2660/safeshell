package cloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
