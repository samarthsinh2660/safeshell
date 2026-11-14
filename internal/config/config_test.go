package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/models"
)

func write(t *testing.T, s string) string {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(s), 0o600)
	return p
}

func TestDefaultsWhenMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "none.json"))
	if err != nil || c.AI.Mode != ai.Auto || c.AI.Local.Model != models.Default || c.AI.Local.GPU != "auto" {
		t.Errorf("%+v %v", c, err)
	}
}

func TestLoadsValuesAndEnvOverridesMode(t *testing.T) {
	p := write(t, `{"ai":{"mode":"hybrid","local":{"model":"qwen2.5-coder-7b","gpu":"off"},"cloud":{"provider":"gemini","model":"gemini-2.5-pro"}}}`)
	c, err := Load(p)
	if err != nil || c.AI.Mode != ai.Hybrid || c.AI.Local.Model != "qwen2.5-coder-7b" || c.AI.Cloud.Provider != "gemini" {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("SAFESHELL_AI", "off")
	if c, _ := LoadEffective(p); c.AI.Mode != ai.Off {
		t.Error("env override")
	}
	if c, _ := Load(p); c.AI.Mode != ai.Hybrid {
		t.Error("Load must return what is saved, not a temporary override")
	}
	t.Setenv("SAFESHELL_AI", "sometimes")
	if _, err := LoadEffective(p); err == nil {
		t.Error("a bad override must be reported")
	}
}

func TestRejectsBadValues(t *testing.T) {
	for name, s := range map[string]string{
		"mode":     `{"ai":{"mode":"always"}}`,
		"json":     `{`,
		"model":    `{"ai":{"local":{"model":"gpt-9000"}}}`,
		"provider": `{"ai":{"cloud":{"provider":"acme"}}}`,
		"gpu":      `{"ai":{"local":{"gpu":"metal"}}}`,
	} {
		if _, err := Load(write(t, s)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestSaveRoundTripsPrivately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	var c Config
	c.AI.Mode = ai.Cloud
	c.AI.Cloud.Provider = "openai"
	c.AI.Local.Model = "qwen3-4b"
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
	got, err := Load(p)
	if err != nil || got.AI.Mode != ai.Cloud || got.AI.Cloud.Provider != "openai" || got.AI.Local.Model != "qwen3-4b" {
		t.Errorf("%+v %v", got, err)
	}
}

func key(t *testing.T, p providers.Provider) string {
	t.Helper()
	k, err := Key(p)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestKeysEnvWinsAndFileIsPrivate(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	p, _ := providers.Find("openai")
	a, _ := providers.Find("anthropic")
	t.Setenv("ANTHROPIC_API_KEY", "")
	if key(t, p) != "" {
		t.Error("no key yet")
	}
	if err := SaveKey(p, "sk-file"); err != nil {
		t.Fatal(err)
	}
	if err := SaveKey(a, "sk-ant"); err != nil {
		t.Fatal(err)
	}
	if key(t, p) != "sk-file" || key(t, a) != "sk-ant" {
		t.Error("keys not kept apart")
	}
	if st, _ := os.Stat(KeysPath()); st.Mode().Perm() != 0o600 {
		t.Errorf("keys mode %v", st.Mode())
	}
	t.Setenv("OPENAI_API_KEY", "sk-env")
	if key(t, p) != "sk-env" {
		t.Error("environment must win")
	}
}

func TestDamagedKeysFileIsNeverOverwritten(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	p, _ := providers.Find("openai")
	os.MkdirAll(filepath.Dir(KeysPath()), 0o700)
	damaged := `{"anthropic": "sk-ant-secret", `
	os.WriteFile(KeysPath(), []byte(damaged), 0o600)
	for name, err := range map[string]error{"Key": func() error { _, err := Key(p); return err }(), "SaveKey": SaveKey(p, "sk-new")} {
		if err == nil || !strings.Contains(err.Error(), "damaged") || !strings.Contains(err.Error(), KeysPath()) {
			t.Errorf("%s: want a damaged-file error naming the path, got %v", name, err)
		}
		if err != nil && strings.Contains(err.Error(), "sk-ant-secret") {
			t.Errorf("%s: error quotes a key", name)
		}
	}
	if b, _ := os.ReadFile(KeysPath()); string(b) != damaged {
		t.Errorf("damaged file was changed: %q", b)
	}
}

func TestKeysFileOthersCanReadIsRefused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "")
	p, _ := providers.Find("openai")
	os.MkdirAll(filepath.Dir(KeysPath()), 0o700)
	os.WriteFile(KeysPath(), []byte(`{"openai": "sk-file"}`), 0o644)
	os.Chmod(KeysPath(), 0o644)
	if k, err := Key(p); err == nil || k != "" || !strings.Contains(err.Error(), "chmod 600 "+KeysPath()) {
		t.Errorf("0644 file: key %q err %v", k, err)
	}
	if err := SaveKey(p, "sk-new"); err == nil {
		t.Error("SaveKey must refuse a file others can read")
	}
	t.Setenv("OPENAI_API_KEY", "sk-env")
	if key(t, p) != "sk-env" {
		t.Error("the environment key needs no file")
	}
}

func TestPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/c")
	if Path() != "/c/safeshell/config.json" {
		t.Error(Path())
	}
}

func TestPathsFallBackToTheHomeFolder(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "/home/alice")
	if Path() != "/home/alice/.config/safeshell/config.json" || KeysPath() != "/home/alice/.config/safeshell/keys.json" {
		t.Error(Path(), KeysPath())
	}
	t.Setenv("XDG_CONFIG_HOME", "/cfg")
	if Path() != "/cfg/safeshell/config.json" {
		t.Error(Path())
	}
}

func TestSaveFailsCleanly(t *testing.T) {
	d := t.TempDir()
	file := filepath.Join(d, "file")
	os.WriteFile(file, nil, 0o600)
	if Save(filepath.Join(file, "sub", "c.json"), Config{}) == nil {
		t.Error("a folder that cannot be made")
	}
	tmpBlocked := filepath.Join(d, "a.json")
	os.Mkdir(tmpBlocked+".tmp", 0o700)
	if Save(tmpBlocked, Config{}) == nil {
		t.Error("a temporary file that cannot be written")
	}
	target := filepath.Join(d, "b.json")
	os.MkdirAll(filepath.Join(target, "inside"), 0o700)
	if Save(target, Config{}) == nil {
		t.Error("a target that cannot be replaced")
	}
}

func TestSaveKeyFailsCleanly(t *testing.T) {
	p, _ := providers.Find("openai")
	t.Setenv("OPENAI_API_KEY", "")

	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.WriteFile(filepath.Join(cfg, "safeshell"), nil, 0o600) // the settings folder is a file
	if SaveKey(p, "sk-1234567890") == nil {
		t.Error("a settings folder that is a file")
	}

	cfg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.Symlink(filepath.Join(cfg, "nowhere"), filepath.Join(cfg, "safeshell")) // a dangling link: the file is "missing" but the folder cannot be made
	if SaveKey(p, "sk-1234567890") == nil {
		t.Error("a folder that cannot be made")
	}

	cfg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(KeysPath()+".tmp", 0o700)
	if SaveKey(p, "sk-1234567890") == nil {
		t.Error("a temporary file that cannot be written")
	}

	cfg = t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	os.MkdirAll(filepath.Join(KeysPath(), "inside"), 0o700)
	if SaveKey(p, "sk-1234567890") == nil {
		t.Error("a key file that cannot be replaced")
	}
}

func TestLoadReportsAnUnreadableFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil { // a folder, not a file
		t.Error("expected a read error")
	}
	t.Setenv("SAFESHELL_AI", "")
	if _, err := LoadEffective(write(t, "{")); err == nil {
		t.Error("LoadEffective must pass on a broken file")
	}
}

func TestSetLearnChangesOnlyItsKey(t *testing.T) {
	p := write(t, `{"x":{"a":[1,2]},"ai":{"mode":"off"}}`)
	if err := SetLearn(p, true); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	var m map[string]any
	want := map[string]any{"x": map[string]any{"a": []any{1.0, 2.0}}, "ai": map[string]any{"mode": "off"}, "learn": true}
	if err := json.Unmarshal(b, &m); err != nil || !reflect.DeepEqual(m, want) {
		t.Errorf("%s %v", b, err)
	}
	q := filepath.Join(t.TempDir(), "new", "c.json") // a missing file gets just the key
	if err := SetLearn(q, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(q); strings.Contains(string(b), "mode") || !strings.Contains(string(b), `"learn": false`) {
		t.Errorf("%s", b)
	}
	bad := write(t, "{") // a damaged file is refused and left as it was
	if err := SetLearn(bad, true); err == nil {
		t.Error("a damaged file must be refused")
	}
	if b, _ := os.ReadFile(bad); string(b) != "{" {
		t.Errorf("damaged file changed: %s", b)
	}
	if err := SetLearn(filepath.Join(p, "x", "c.json"), true); err == nil { // under a file
		t.Error("expected an error under a file")
	}
	if os.Getuid() != 0 { // a folder that cannot be written to
		ro := filepath.Join(t.TempDir(), "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		if err := SetLearn(filepath.Join(ro, "c.json"), true); err == nil {
			t.Error("a read-only folder must be an error")
		}
		if err := SetLearn(filepath.Join(ro, "new", "c.json"), true); err == nil {
			t.Error("a folder that cannot be made must be an error")
		}
	}
}
