package ai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fast(t *testing.T) {
	old := RetryDelay
	RetryDelay = time.Millisecond
	t.Cleanup(func() { RetryDelay = old })
}

func get(url string) func() (*http.Request, error) {
	return func() (*http.Request, error) { return http.NewRequest(http.MethodGet, url, nil) }
}

func TestDoRetriesBusyAnswersThenSucceeds(t *testing.T) {
	fast(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			http.Error(w, "high demand", 503)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), http.DefaultClient, get(srv.URL))
	if err != nil || resp.StatusCode != 200 || calls.Load() != 3 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
	resp.Body.Close()
}

func TestDoGivesUpAfterThreeTriesAndReturnsTheLastStatus(t *testing.T) {
	fast(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "busy", 429) }))
	defer srv.Close()
	resp, err := Do(context.Background(), http.DefaultClient, get(srv.URL))
	if err != nil || resp.StatusCode != 429 || calls.Load() != 3 {
		t.Fatalf("err %v calls %d", err, calls.Load())
	}
	resp.Body.Close()
}

func TestDoNeverRetriesAnswersThatWillNotChange(t *testing.T) {
	fast(t)
	for _, code := range []int{400, 401, 403, 404} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "no", code) }))
		resp, err := Do(context.Background(), http.DefaultClient, get(srv.URL))
		srv.Close()
		if err != nil || resp.StatusCode != code || calls.Load() != 1 {
			t.Errorf("%d: err %v calls %d", code, err, calls.Load())
		}
		resp.Body.Close()
	}
}

func TestDoRetriesDroppedConnectionsAndStopsWhenCancelled(t *testing.T) {
	fast(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			panic(http.ErrAbortHandler) // drops the connection
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	resp, err := Do(context.Background(), http.DefaultClient, get(srv.URL))
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("dropped connection not retried: %v", err)
	}
	resp.Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Do(ctx, http.DefaultClient, get("http://127.0.0.1:1")); err == nil {
		t.Error("a cancelled call must stop")
	}
}

func TestStatusErrorsTellThePersonWhatToDoAndNeverQuoteTheBody(t *testing.T) {
	for code, want := range map[int]string{401: "API key", 403: "API key", 404: "retired", 400: "model name", 429: "limit", 500: "HTTP 500"} {
		got := StatusError("gemini:x", code).Error()
		if !strings.Contains(got, want) || !strings.Contains(got, "gemini:x") {
			t.Errorf("%d: %q lacks %q", code, got, want)
		}
	}
}

func TestDoWaitsAsLongAsTheServerAsksButNoLongerThanTheCap(t *testing.T) {
	old := RetryDelay
	RetryDelay = time.Millisecond
	defer func() { RetryDelay = old }()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, "slow down", 429)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()
	start := time.Now()
	resp, err := Do(context.Background(), http.DefaultClient, get(srv.URL))
	if err != nil || resp.StatusCode != 200 || time.Since(start) < 900*time.Millisecond {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
	resp.Body.Close()
	if d := retryAfter(&http.Response{Header: http.Header{"Retry-After": {"9999"}}}); d != 30*time.Second {
		t.Errorf("cap: %v", d)
	}
	if retryAfter(&http.Response{Header: http.Header{"Retry-After": {"Wed, 21 Oct 2026 07:28:00 GMT"}}}) != 0 || retryAfter(nil) != 0 {
		t.Error("dates and nil are ignored")
	}
}

func TestPostSendsJSONWithHeadersAndReturnsTheBody(t *testing.T) {
	var got map[string]any
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header
		json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte("reply"))
	}))
	defer srv.Close()
	out, err := Post(context.Background(), http.DefaultClient, "svc", srv.URL, map[string]string{"X-Key": "k"}, map[string]any{"a": 1})
	if err != nil || string(out) != "reply" {
		t.Fatalf("%q %v", out, err)
	}
	if hdr.Get("Content-Type") != "application/json" || hdr.Get("X-Key") != "k" || got["a"] != float64(1) {
		t.Errorf("hdr %v body %v", hdr, got)
	}
}

func TestPostErrors(t *testing.T) {
	fast(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "your prompt was: secret", 401)
	}))
	defer srv.Close()
	_, err := Post(context.Background(), http.DefaultClient, "svc", srv.URL, nil, map[string]any{})
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "svc") || !strings.Contains(err.Error(), "401") {
		t.Errorf("status: %v", err)
	}
	if _, err := Post(context.Background(), http.DefaultClient, "svc", srv.URL, nil, make(chan int)); err == nil {
		t.Error("a body that is not JSON should fail")
	}
	if _, err := Post(context.Background(), http.DefaultClient, "svc", "http://bad host/", nil, map[string]any{}); err == nil {
		t.Error("a bad address should fail")
	}
}

func TestStatusErrorTexts(t *testing.T) {
	for code, want := range map[int]string{400: "model name", 401: "API key", 403: "API key", 404: "retired", 429: "quota", 500: "HTTP 500"} {
		if err := StatusError("svc", code); !strings.Contains(err.Error(), want) {
			t.Errorf("%d: %v", code, err)
		}
	}
}

func TestDecodeJSON(t *testing.T) {
	var v struct{ A int }
	if err := DecodeJSON("sure!\n```json\n{\"A\": 7}\n```", &v); err != nil || v.A != 7 {
		t.Errorf("%+v %v", v, err)
	}
	for _, bad := range []string{"", "no braces", "} {", "{not json}", `{"A":"x"}`} {
		if err := DecodeJSON(bad, &v); !errors.Is(err, ErrNotJSON) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}
