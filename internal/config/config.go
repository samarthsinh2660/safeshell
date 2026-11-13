// Package config loads and saves the user's settings under ~/.config/safeshell.
package config

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// Config holds every setting. The zero value is valid.
type Config struct {
	// Learn turns on learning mode: one line after each command that passes
	// without a warning, saying what it does and the safer form.
	Learn bool `json:"learn"`
	AI    struct {
		// Mode: auto (local if its files are installed), off, local, cloud, or
		// hybrid (local first, cloud when local fails).
		Mode  ai.Mode `json:"mode"`
		Local struct {
			Model string `json:"model"` // a models.Catalog id
			GPU   string `json:"gpu"`   // auto, cuda, vulkan, cpu
		} `json:"local"`
		Cloud struct {
			Provider string `json:"provider"` // anthropic, openai, gemini
			Model    string `json:"model"`    // empty = the provider's default
		} `json:"cloud"` // keys live in keys.json or the environment, never here
	} `json:"ai"`
}

func dir() string { return xdg.Dir("XDG_CONFIG_HOME", ".config") }

// Path is the config file.
func Path() string { return filepath.Join(dir(), "config.json") }

// LoadEffective is Load plus the SAFESHELL_AI override. Use it to decide how to
// behave; never save its result, or a temporary override becomes permanent.
func LoadEffective(path string) (Config, error) {
	c, err := Load(path)
	if err != nil {
		return c, err
	}
	if m := os.Getenv("SAFESHELL_AI"); m != "" {
		switch ai.Mode(m) {
		case ai.Auto, ai.Off, ai.Local, ai.Cloud, ai.Hybrid:
			c.AI.Mode = ai.Mode(m)
		default:
			return Config{}, errors.New("SAFESHELL_AI must be auto, off, local, cloud or hybrid, not " + m)
		}
	}
	return c, nil
}

// Load reads path exactly as saved (plus defaults). A missing file gives defaults.
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return c, err
	default:
		if err := json.Unmarshal(b, &c); err != nil {
			return Config{}, errors.New(path + ": " + err.Error())
		}
	}
	switch c.AI.Mode {
	case "":
		c.AI.Mode = ai.Auto
	case ai.Auto, ai.Off, ai.Local, ai.Cloud, ai.Hybrid:
	default:
		return Config{}, errors.New("ai.mode must be auto, off, local, cloud or hybrid, not " + string(c.AI.Mode))
	}
	if c.AI.Local.Model == "" {
		c.AI.Local.Model = models.Default
	}
	if _, ok := models.Find(c.AI.Local.Model); !ok {
		return Config{}, errors.New("ai.local.model " + c.AI.Local.Model + " is not a known model (see /models)")
	}
	switch c.AI.Local.GPU {
	case "":
		c.AI.Local.GPU = "auto"
	case "auto", "cuda", "vulkan", "cpu", "on", "off":
	default:
		return Config{}, errors.New("ai.local.gpu must be auto, cuda, vulkan or cpu, not " + c.AI.Local.GPU)
	}
	if p := c.AI.Cloud.Provider; p != "" {
		if _, ok := providers.Find(p); !ok {
			return Config{}, errors.New("ai.cloud.provider must be anthropic, openai or gemini, not " + p)
		}
	}
	return c, nil
}

// Save writes the config privately and atomically.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SetLearn changes only the "learn" key of the file at path and keeps every
// other key exactly as it is, including keys this version does not know, so
// turning learning mode on never freezes today's defaults into the file. A
// damaged file is refused and left alone.
func SetLearn(path string, on bool) error {
	if _, err := Load(path); err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &raw) // Load already proved it is a JSON object
	}
	raw["learn"] = json.RawMessage(strconv.FormatBool(on))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(raw, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// KeysPath is the file holding API keys (mode 0600).
func KeysPath() string { return filepath.Join(dir(), "keys.json") }

// Key returns the API key for a provider: the environment wins, then keys.json.
func Key(p providers.Provider) string {
	if k := p.KeyFromEnv(); k != "" {
		return k
	}
	b, err := os.ReadFile(KeysPath())
	if err != nil {
		return ""
	}
	var m map[string]string
	_ = json.Unmarshal(b, &m)
	return m[p.ID]
}

// SaveKey stores a provider's key in keys.json, readable only by the user.
func SaveKey(p providers.Provider, key string) error {
	m := map[string]string{}
	if b, err := os.ReadFile(KeysPath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	m[p.ID] = key
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	tmp := KeysPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, KeysPath())
}
