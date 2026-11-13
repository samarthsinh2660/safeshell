package ai

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(t *testing.T, code int, body string) (*httptest.Server, *http.Header) {
	var hdr http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header
		w.WriteHeader(code)
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &hdr
}

func TestFetchModelsReadsIdsAndNames(t *testing.T) {
	srv, hdr := serve(t, 200, `{"data":[{"id":"a","display_name":"Model A"},{"id":""},{"id":"b"}]}`)
	got, err := FetchModels(context.Background(), nil, "x", srv.URL, map[string]string{"X-Key": "k"})
	if err != nil || len(got) != 2 || got[0] != (ModelInfo{ID: "a", Name: "Model A"}) || got[1].ID != "b" {
		t.Fatalf("%v %v", got, err)
	}
	if (*hdr).Get("X-Key") != "k" {
		t.Error("key header not sent")
	}
}

func TestFetchModelsErrorsNeverEchoTheBody(t *testing.T) {
	for code, want := range map[int]string{401: "rejected the API key", 429: "rate or quota", 404: "could not list its models (HTTP 404)"} {
		srv, _ := serve(t, code, "secret-body")
		_, err := FetchModels(context.Background(), nil, "svc", srv.URL, nil)
		if err == nil || !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%d: %v", code, err)
		}
	}
	srv, _ := serve(t, 200, "not json secret")
	if _, err := FetchModels(context.Background(), nil, "svc", srv.URL, nil); err == nil || !strings.Contains(err.Error(), "could not be read") || strings.Contains(err.Error(), "secret") {
		t.Errorf("bad json: %v", err)
	}
}

func TestFetchModelsReportsUnreachableAndBadAddresses(t *testing.T) {
	if _, err := FetchModels(context.Background(), nil, "svc", "http://127.0.0.1:1", nil); err == nil || !strings.Contains(err.Error(), "check your network") {
		t.Errorf("unreachable: %v", err)
	}
	if _, err := FetchModels(context.Background(), nil, "svc", "http://[::1", nil); err == nil {
		t.Error("bad address")
	}
}
