package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFindModel(t *testing.T) {
	for _, name := range []string{"", "base.en", "base", "small"} {
		m, err := FindModel(name)
		if err != nil || m.SHA == "" || len(m.SHA) != 64 || m.Size == 0 || m.File == "" {
			t.Errorf("%q: %+v %v", name, m, err)
		}
	}
	if m, _ := FindModel(""); m.Name != DefaultModel || !m.English {
		t.Errorf("default is %+v", m)
	}
	if _, err := FindModel("tiny"); err == nil || !strings.Contains(err.Error(), "Choose base.en, base or small") {
		t.Errorf("%v", err)
	}
	small, _ := FindModel("small")
	if small.URL() != "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin" {
		t.Error(small.URL())
	}
}

func TestCheckLanguage(t *testing.T) {
	for _, ok := range []string{"", "auto", "en", "hi", "es", "haw"} {
		if err := CheckLanguage(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"e", "EN", "english", "de-", "h1", "auto "} {
		if err := CheckLanguage(bad); err == nil || !strings.Contains(err.Error(), "language code") {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestModelLanguage(t *testing.T) {
	en, _ := FindModel("base.en")
	base, _ := FindModel("base")
	for _, c := range []struct {
		m          Model
		lang, want string
	}{{en, "", "en"}, {en, "hi", "en"}, {base, "", "auto"}, {base, "auto", "auto"}, {base, "hi", "hi"}} {
		if got := c.m.Language(c.lang); got != c.want {
			t.Errorf("%s %q: %q", c.m.Name, c.lang, got)
		}
	}
}

func TestWhisperPassesTheLanguage(t *testing.T) {
	for lang, want := range map[string]string{"": "en", "hi": "hi", "auto": "auto"} {
		args := WhisperCLI{Model: "m", Lang: lang}.Args("w.wav", "out")
		if !reflect.DeepEqual(args[4:6], []string{"-l", want}) {
			t.Errorf("%q: %v", lang, args)
		}
	}
}

func TestEnsureModelDownloadsTheChosenModel(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = r.URL.Path
		http.NotFound(w, r)
	}))
	defer srv.Close()
	s := setup(t)
	s.Model, _ = FindModel("small")
	s.Client = &http.Client{Transport: redirect{srv.URL}}
	if !strings.HasSuffix(s.ModelPath(), "ggml-small.bin") || s.Status().ModelReady {
		t.Fatalf("%s", s.ModelPath())
	}
	var out strings.Builder
	s.Out = &out
	if _, err := s.EnsureModel(context.Background()); err == nil {
		t.Error("the fake server has no model")
	}
	if !strings.HasSuffix(asked, "/ggml-small.bin") || !strings.Contains(out.String(), "multilingual small speech model (487 MB)") {
		t.Errorf("%q %q", asked, out.String())
	}
}

func TestEnsureModelKeepsTheOldFileAndSavesAVerifiedOne(t *testing.T) {
	data := []byte("tiny model")
	sum := sha256.Sum256(data)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(data) }))
	defer srv.Close()
	s := setup(t)
	old := s.ModelPath()
	_ = os.MkdirAll(filepath.Dir(old), 0o755)
	_ = os.WriteFile(old, []byte("old"), 0o600)
	s.Model = Model{Name: "tiny", Title: "tiny", File: "ggml-tiny.bin", Size: int64(len(data)), SHA: hex.EncodeToString(sum[:])}
	s.Client = &http.Client{Transport: redirect{srv.URL}}
	got, err := s.EnsureModel(context.Background())
	if err != nil || got != s.ModelPath() || !s.Status().ModelReady {
		t.Fatalf("%q %v", got, err)
	}
	if b, _ := os.ReadFile(old); string(b) != "old" {
		t.Error("the earlier model must stay")
	}
}
