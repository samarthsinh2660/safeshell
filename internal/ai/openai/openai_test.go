package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompleteSendsChatRequestAndReturnsContent(t *testing.T) {
	var got map[string]any
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"hello"}}]}`))
	}))
	defer srv.Close()
	zero := 0.0
	out, err := Client{Label: "x", URL: srv.URL + "/v1/", Key: "k", Model: "m", Tokens: "max_completion_tokens", Temperature: &zero}.Complete(context.Background(), "sys", "usr")
	if err != nil || out != "hello" {
		t.Fatalf("%q %v", out, err)
	}
	if auth != "Bearer k" || path != "/v1/chat/completions" || got["model"] != "m" || got["max_completion_tokens"] == nil || got["max_tokens"] != nil || got["temperature"] != 0.0 {
		t.Errorf("auth %q path %q body %v", auth, path, got)
	}
}

func TestNoKeyMeansNoAuthHeaderAndNoTemperatureUnlessSet(t *testing.T) {
	var got map[string]any
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	Client{URL: srv.URL, Model: "m"}.Complete(context.Background(), "", "")
	if auth != "" || got["temperature"] != nil || got["max_tokens"] == nil {
		t.Errorf("auth %q body %v", auth, got)
	}
}

func TestErrorsNeverEchoTheBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("e") != "" {
			w.Write([]byte(`{"choices":[]}`))
			return
		}
		http.Error(w, "your prompt: rm -rf secret", 401)
	}))
	defer srv.Close()
	_, err := Client{Label: "gpt", URL: srv.URL, Model: "m"}.Complete(context.Background(), "", "")
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "401") {
		t.Errorf("%v", err)
	}
	if _, err := (Client{Label: "gpt", URL: srv.URL + "/x?e=1", Model: "m"}).Complete(context.Background(), "", ""); err == nil {
		t.Error("empty choices accepted")
	}
}

func TestReasoningEffortIsSentOnlyWhenSet(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	Client{URL: srv.URL, Model: "m", Reasoning: "low"}.Complete(context.Background(), "", "")
	if got["reasoning_effort"] != "low" {
		t.Errorf("body %v", got)
	}
	got = nil
	Client{URL: srv.URL, Model: "m"}.Complete(context.Background(), "", "")
	if _, ok := got["reasoning_effort"]; ok {
		t.Errorf("body %v", got)
	}
}

func TestListModelsSendsTheBearerKey(t *testing.T) {
	var auth, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		w.Write([]byte(`{"data":[{"id":"gpt-x"}]}`))
	}))
	defer srv.Close()
	got, err := Client{Label: "gpt", URL: srv.URL + "/v1/", Key: "k"}.ListModels(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != "gpt-x" || auth != "Bearer k" || path != "/v1/models" {
		t.Fatalf("%v %v %q %q", got, err, auth, path)
	}
}

func TestName(t *testing.T) {
	if got := (Client{Label: "gpt:m"}).Name(); got != "gpt:m" {
		t.Error(got)
	}
}
