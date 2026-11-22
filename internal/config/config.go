// Package config loads and saves the user's settings under ~/.config/safeshell.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/policy"
	"github.com/samarthsinh2660/safeshell/internal/voice"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// Config holds every setting. The zero value is valid.
type Config struct {
	// Learn turns on learning mode: one line after each command that passes
	// without a warning, saying what it does and the safer form.
	Learn bool `json:"learn"`
	// Autorun lets the assistant run a safe command (R0 or R1, a program the rules
	// know) at once, without the "Run it?" question. Anything riskier still asks.
	Autorun bool `json:"autorun"`
	// Plain turns on plain output for screen readers: no colour or columns,
	// one full sentence per line and the choices spelled out in words.
	Plain bool `json:"plain"`
	// Strict makes the shell hook fail closed: when SafeShell itself breaks,
	// only plain read-only commands run, and SAFESHELL_OFF is refused. A team
	// policy can turn it on for everyone; nothing turns it off from there.
	Strict bool `json:"strict"`
	// Snapshots caps what undo keeps. After each new snapshot the oldest are
	// deleted until none is older than KeepDays and all together fit in MaxGiB.
	Snapshots struct {
		KeepDays int `json:"keep_days"` // default 30
		MaxGiB   int `json:"max_gib"`   // default 5
	} `json:"snapshots"`
	// Audit.Journal also sends every audit entry to the systemd journal
	// (journalctl -t safeshell). The audit file is written either way.
	Audit struct {
		Journal bool `json:"journal"`
	} `json:"audit"`
	// Hosts maps a host name pattern (* matches any text) to a label, prod or
	// staging. A change made on a prod host over ssh is raised one level.
	Hosts map[string]string `json:"hosts"`
	// Protect lists folders (full paths, or starting with ~/) that nothing may
	// change without being blocked, such as ["~/thesis", "/srv/data"].
	Protect []string `json:"protect"`
	// Trusted lists the commands the person chose "always run this here" for:
	// the exact line and the full path of the folder. Only R2 lines that were
	// really observed are ever let through; see policy.DecideTrusted.
	Trusted []policy.Trust `json:"trusted"`
	// Voice is the speech model (base.en, base or small) and the language it
	// listens for (auto or a code such as hi); empty means base.en and its default.
	Voice struct {
		Model    string `json:"model"`
		Language string `json:"language"`
	} `json:"voice"`
	AI struct {
		// Mode: auto (local if its files are installed), off, local, cloud, or
		// hybrid (local first, cloud when local fails).
		Mode  ai.Mode `json:"mode"`
		Local struct {
			Model string `json:"model"` // a models.Catalog id
			GPU   string `json:"gpu"`   // auto, cuda, vulkan, cpu (on and off also work)
			// Engine: bundled (download the pinned llama-server, default) or system (use llama-server found on PATH).
			Engine string `json:"engine"`
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
		return Config{}, errors.New("ai.local.gpu must be auto, cuda, vulkan, cpu, on or off, not " + c.AI.Local.GPU)
	}
	switch c.AI.Local.Engine {
	case "":
		c.AI.Local.Engine = "bundled"
	case "bundled", "system":
	default:
		return Config{}, errors.New("ai.local.engine must be bundled or system, not " + c.AI.Local.Engine)
	}
	if _, err := voice.FindModel(c.Voice.Model); err != nil {
		return Config{}, errors.New("voice.model: " + err.Error())
	}
	if err := voice.CheckLanguage(c.Voice.Language); err != nil {
		return Config{}, errors.New("voice.language: " + err.Error())
	}
	if c.Snapshots.KeepDays < 0 || c.Snapshots.MaxGiB < 0 {
		return Config{}, errors.New("snapshots.keep_days and snapshots.max_gib must be whole numbers above zero")
	}
	if c.Snapshots.KeepDays == 0 {
		c.Snapshots.KeepDays = 30
	}
	if c.Snapshots.MaxGiB == 0 {
		c.Snapshots.MaxGiB = 5
	}
	if err := policy.ValidateHosts(c.Hosts); err != nil {
		return Config{}, err
	}
	for _, f := range c.Protect {
		if f != "~" && !strings.HasPrefix(f, "~/") && !strings.HasPrefix(f, "/") {
			return Config{}, fmt.Errorf("protect: %q must be a full path such as \"/srv/data\" or start with ~/ such as \"~/thesis\"", f)
		}
	}
	// A trusted entry without a command or a full folder path is dropped, not an error:
	// trust is only a convenience, so a bad entry just means SafeShell asks again, and
	// the rest of the settings (and safeshell trust itself) keep working.
	c.Trusted = slices.DeleteFunc(c.Trusted, func(t policy.Trust) bool {
		return t.Line == "" || !strings.HasPrefix(t.Dir, "/") || t.Dir != filepath.Clean(t.Dir)
	})
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
	return writePrivate(path, b)
}

// writePrivate writes b to a fresh temporary file next to path, makes it
// private before any content goes in, and renames it into place. A leftover
// file with a loose mode can never leak its permissions into path.
func writePrivate(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// SetLearn changes only the "learn" key of the file at path and keeps every
// other key exactly as it is, including keys this version does not know, so
// turning learning mode on never freezes today's defaults into the file. A
// damaged file is refused and left alone.
func SetLearn(path string, on bool) error {
	return setKey(path, "learn", json.RawMessage(strconv.FormatBool(on)))
}

// SetAutorun changes only the "autorun" key, the same way as SetLearn.
func SetAutorun(path string, on bool) error {
	return setKey(path, "autorun", json.RawMessage(strconv.FormatBool(on)))
}

// SetVoice records the speech model and language the same way: only the
// voice key changes.
func SetVoice(path, model, language string) error {
	v, _ := json.Marshal(map[string]string{"model": model, "language": language})
	return setKey(path, "voice", v)
}

// SetTrusted records the trusted commands the same way: only the trusted key changes.
func SetTrusted(path string, list []policy.Trust) error {
	v, _ := json.Marshal(list)
	return setKey(path, "trusted", v)
}

// setKey changes one top-level key and keeps everything else as it is.
func setKey(path, key string, value json.RawMessage) error {
	if _, err := Load(path); err != nil {
		return err
	}
	raw := map[string]json.RawMessage{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &raw) // Load already proved it is a JSON object
	}
	raw[key] = value
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(raw, "", "  ")
	return writePrivate(path, b)
}

// KeysPath is the file holding API keys (mode 0600).
func KeysPath() string { return filepath.Join(dir(), "keys.json") }

// readKeys loads keys.json. A missing file is empty. A damaged file, or one
// other users can read, is an error so it is never overwritten or trusted.
func readKeys() (map[string]string, error) {
	path := KeysPath()
	m := map[string]string{}
	st, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s can be read by other users. Run: chmod 600 %s", path, path)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s is damaged. Fix it, or delete it and add your keys again", path)
	}
	return m, nil
}

// Key returns the API key for a provider: the environment wins, then keys.json.
// A missing key is "" with no error; an unusable keys.json is an error.
func Key(p providers.Provider) (string, error) {
	if k := p.KeyFromEnv(); k != "" {
		return k, nil
	}
	m, err := readKeys()
	return m[p.ID], err
}

// SaveKey stores a provider's key in keys.json, readable only by the user. It
// refuses to touch a damaged file, so the other keys in it are not lost.
func SaveKey(p providers.Provider, key string) error {
	m, err := readKeys()
	if err != nil {
		return err
	}
	m[p.ID] = key
	if err := os.MkdirAll(dir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	return writePrivate(KeysPath(), b)
}
