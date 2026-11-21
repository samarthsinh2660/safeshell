package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
	"github.com/samarthsinh2660/safeshell/internal/voice"
)

// modelSetup is a setup with every tool and downloaded copies of the named
// models (empty sparse files of the pinned size), so nothing is fetched.
func modelSetup(t *testing.T, names ...string) voice.Setup {
	t.Helper()
	s := fakeSetup(t, "whisper-cli", "arecord")
	for _, n := range names {
		m, _ := voice.FindModel(n)
		s.Model = m
		_ = os.MkdirAll(filepath.Dir(s.ModelPath()), 0o755)
		f, _ := os.Create(s.ModelPath())
		_ = f.Truncate(m.Size)
		f.Close()
		_ = runtime.MarkVerified(s.ModelPath(), m.SHA)
	}
	s.Model = voice.Model{}
	return s
}

func TestVoiceSetupChoosesModelAndLanguage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := modelSetup(t, "base.en", "small")
	useSetup(t, s)
	code, out, errOut := runSS("voice", "setup", "--model", "small", "--language", "hi")
	if code != 0 || !strings.Contains(out, "ggml-small.bin (language: hi)") || errOut != "" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if c, _ := config.Load(config.Path()); c.Voice.Model != "small" || c.Voice.Language != "hi" {
		t.Errorf("%+v", c.Voice)
	}
	if _, err := os.Stat(s.Dirs.ModelPath("ggml-base.en.bin")); err != nil {
		t.Errorf("the old model must be kept: %v", err)
	}
	_, out, _ = runSS("voice", "status")
	if !strings.Contains(out, "model:    small, downloaded") || !strings.Contains(out, "language: hi") {
		t.Errorf("%q", out)
	}
	_, tr, err := newVoice()
	if err != nil || tr.(voice.WhisperCLI).Lang != "hi" || !strings.HasSuffix(tr.(voice.WhisperCLI).Model, "ggml-small.bin") {
		t.Errorf("%v %v", tr, err)
	}

	// Setting up again without options keeps the choice.
	if code, out, _ := runSS("voice", "setup"); code != 0 || !strings.Contains(out, "(language: hi)") {
		t.Errorf("%d %q", code, out)
	}
	// Going back to English forgets the language; a multilingual model then starts at auto.
	if code, out, _ := runSS("voice", "setup", "--model", "base.en"); code != 0 || !strings.Contains(out, "(language: en)") {
		t.Errorf("%d %q", code, out)
	}
	if c, _ := config.Load(config.Path()); c.Voice.Model != "base.en" || c.Voice.Language != "" {
		t.Errorf("%+v", c.Voice)
	}
	if code, out, _ := runSS("voice", "setup", "--model", "small"); code != 0 || !strings.Contains(out, "(language: auto)") {
		t.Errorf("%d %q", code, out)
	}
	_, tr, _ = newVoice()
	if tr.(voice.WhisperCLI).Lang != "auto" {
		t.Errorf("%v", tr)
	}
}

func TestVoiceSetupRejectsBadChoices(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	useSetup(t, modelSetup(t, "base.en"))
	for args, want := range map[string]string{
		"--model tiny":                  `unknown speech model "tiny". Choose base.en, base or small`,
		"--language EN":                 "not a language code",
		"--language english":            "not a language code",
		"--model base.en --language hi": "only understands English",
		"--model small --language de-":  "not a language code",
	} {
		code, _, errOut := runSS(append([]string{"voice", "setup"}, strings.Fields(args)...)...)
		if code != 1 || !strings.Contains(errOut, want) {
			t.Errorf("%s: %d %q", args, code, errOut)
		}
	}
	if code, _, _ := runSS("voice", "setup", "--language", "en"); code != 0 {
		t.Errorf("en with the English model is fine: %d", code)
	}
	for _, a := range []string{"--model", "--language"} {
		if code, _, e := runSS("voice", "setup", a); code != 2 || !strings.Contains(e, "Usage:") {
			t.Errorf("%s: %d %q", a, code, e)
		}
	}
}

func TestVoiceSetupNeedsAReadableConfigAndSavesIt(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	useSetup(t, modelSetup(t, "base.en"))
	if os.Getuid() != 0 { // a config folder that cannot be written to
		_ = os.MkdirAll(filepath.Join(cfg, "safeshell"), 0o500)
		if code, _, e := runSS("voice", "setup"); code != 1 || !strings.Contains(e, "could not be saved") {
			t.Errorf("%d %q", code, e)
		}
		_ = os.Chmod(filepath.Join(cfg, "safeshell"), 0o700)
	}
	_ = os.MkdirAll(filepath.Join(cfg, "safeshell"), 0o700)
	_ = os.WriteFile(config.Path(), []byte(`{"voice":{"model":"huge"}}`), 0o600)
	for _, args := range [][]string{{"voice", "setup"}, {"voice", "status"}, {"voice", "--text"}} {
		if code, _, e := runSS(args...); code != 1 || !strings.Contains(e, "voice.model") {
			t.Errorf("%v: %d %q", args, code, e)
		}
	}
}
