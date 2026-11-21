package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/config"
)

func writeConfig(t *testing.T, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.Path()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.Path(), []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAIStatusSaysWhyAIIsOff(t *testing.T) {
	cases := []struct {
		name, cfg string
		b         ai.Backend
		err       error
		want      string
	}{
		{"off", `{"ai":{"mode":"off"}}`, nil, nil, "AI is turned off in your settings"},
		{"cloud without key", `{"ai":{"mode":"cloud"}}`, nil, errors.New("no API key for Claude: type /models"), "no API key for Claude"},
		{"local not installed", `{"ai":{"mode":"local"}}`, fakeAI{}, nil, "The local model or engine is not installed. Run: safeshell ai setup"},
		{"auto nothing installed", `{}`, nil, nil, "The local model or engine is not installed. " + aiOffHint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aiDirs(t)
			writeConfig(t, tc.cfg)
			useAI(t, tc.b, tc.err)
			_, out, _ := runSS("ai", "status")
			if !strings.Contains(out, "AI: off") || !strings.Contains(out, tc.want) {
				t.Errorf("%q", out)
			}
		})
	}
}

func TestSettingsTyposShowInDoctorAndAIStatusOnly(t *testing.T) {
	aiDirs(t)
	writeConfig(t, `{"ai":{"local":{"modle":"x"}}}`)
	want := "unknown setting ai.local.modle is ignored: did you mean ai.local.model?"
	var b strings.Builder
	doctorConfig(&b)
	if !strings.Contains(b.String(), want) {
		t.Errorf("doctor: %q", b.String())
	}
	useAI(t, nil, nil)
	if _, out, _ := runSS("ai", "status"); !strings.Contains(out, "Warning: "+want) {
		t.Errorf("ai status: %q", out)
	}
	if _, out, e := runSS("check", "ls"); strings.Contains(out+e, "modle") {
		t.Errorf("other commands must stay quiet: %q %q", out, e)
	}
}
