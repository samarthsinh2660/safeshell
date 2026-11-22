package main

import (
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

var vulkanLine = regexp.MustCompile(`(Vulkan loader\s+)(true|false)`)

// aiGolden puts the machine in one fixed AI state and writes the doctor AI
// section and `ai status` to testdata/ai_<name>_{doctor,status}.golden. Paths and
// whether this machine has Vulkan are made fixed so the files match anywhere.
// Update them with: go test ./cmd/safeshell -run AIGolden -update
func aiGolden(t *testing.T, name string, setup func(t *testing.T, c *config.Config, home string)) {
	t.Helper()
	home := doctorHome(t)
	t.Setenv("PATH", t.TempDir())
	oldDriver, oldArch := nvidiaDriver, hostArch
	t.Cleanup(func() { nvidiaDriver, hostArch = oldDriver, oldArch })
	nvidiaDriver, hostArch = func() int { return 0 }, goruntime.GOARCH
	oldBackend := backend
	t.Cleanup(func() { backend = oldBackend })
	backend = func() (ai.Backend, error) { // TestMain turns the AI off; this is the real choice, which never reaches the network
		c, err := config.LoadEffective(config.Path())
		if err != nil {
			return nil, err
		}
		return buildBackend(c, runtime.DefaultDirs())
	}
	c := config.Config{}
	c.AI.Mode, c.AI.Local.GPU = ai.Auto, "cpu"
	setup(t, &c, home)
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
	fix := func(s string) string {
		s = strings.ReplaceAll(s, home, "<home>")
		return vulkanLine.ReplaceAllString(s, "${1}<varies>")
	}
	var b strings.Builder
	doctorAI(&b)
	doctorCloud(&b)
	golden(t, "ai_"+name+"_doctor", fix(b.String()))
	_, out, _ := runSS("ai", "status")
	golden(t, "ai_"+name+"_status", fix(out))
}

// installModel writes a complete, verified copy of the default model.
func installModel(t *testing.T) {
	t.Helper()
	m, _ := models.Find(models.Default)
	p := runtime.DefaultDirs().ModelPath(m.File)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Truncate(m.Size)
	f.Close()
	_ = runtime.MarkVerified(p, m.SHA)
}

func TestAIGoldenOff(t *testing.T) {
	aiGolden(t, "off", func(t *testing.T, c *config.Config, _ string) { c.AI.Mode = ai.Off })
}

func TestAIGoldenLocalReadyAndRunning(t *testing.T) {
	aiGolden(t, "local_ready", func(t *testing.T, c *config.Config, _ string) {
		installModel(t)
		d := runtime.DefaultDirs()
		bin := d.ServerBin(runtime.Candidates(goruntime.GOARCH, "cpu")[0])
		if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		m, _ := models.Find(models.Default)
		exe, _ := os.Executable() // this test binary stands in for the engine
		state := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"port":1,"model":"` + d.ModelPath(m.File) + `","bin":"` + exe + `"}`
		if err := os.MkdirAll(d.State, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d.State, "llama.json"), []byte(state), 0o600); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAIGoldenSystemEngine(t *testing.T) {
	aiGolden(t, "system_engine", func(t *testing.T, c *config.Config, home string) {
		installModel(t)
		bin := filepath.Join(home, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(bin, "llama-server"), []byte("#!/bin/sh\necho 'version: 9999 (abcdef0)'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", bin)
		c.AI.Mode, c.AI.Local.Engine = ai.Local, "system"
	})
}

func TestAIGoldenCloudKeyFromEnvironment(t *testing.T) {
	aiGolden(t, "cloud_env_key", func(t *testing.T, c *config.Config, _ string) {
		c.AI.Mode, c.AI.Cloud.Provider = ai.Cloud, "anthropic"
		t.Setenv("ANTHROPIC_API_KEY", "sk-golden-secret")
	})
}

func TestAIGoldenCloudKeyInKeysFile(t *testing.T) {
	aiGolden(t, "cloud_keys_file", func(t *testing.T, c *config.Config, _ string) {
		c.AI.Mode, c.AI.Cloud.Provider = ai.Cloud, "gemini"
		p, _ := providers.Find("gemini")
		if err := config.SaveKey(p, "golden-secret"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAIGoldenCloudMissingKey(t *testing.T) {
	aiGolden(t, "cloud_no_key", func(t *testing.T, c *config.Config, _ string) {
		c.AI.Mode, c.AI.Cloud.Provider = ai.Cloud, "anthropic"
	})
}
