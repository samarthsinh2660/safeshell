package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// aiDirs points every SafeShell folder at fresh temporary ones.
func aiDirs(t *testing.T) {
	t.Helper()
	for _, e := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"} {
		t.Setenv(e, t.TempDir())
	}
	t.Setenv("SAFESHELL_AI", "")
}

func TestAIStatusSaysWhatIsOffAndWhatIsDownloaded(t *testing.T) {
	aiDirs(t)
	useAI(t, nil, nil)
	code, out, _ := runSS("ai", "status")
	if code != 0 || !strings.Contains(out, "AI: off") || !strings.Contains(out, aiOffHint) || !strings.Contains(out, "not downloaded") || !strings.Contains(out, "Local engine: not running") {
		t.Errorf("%d %q", code, out)
	}

	useAI(t, fakeAI{}, nil)
	d := runtime.DefaultDirs()
	m, _ := models.Find(models.Default)
	p := d.ModelPath(m.File)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(p)
	_ = f.Truncate(m.Size)
	f.Close()
	_, out, _ = runSS("ai", "status")
	if !strings.Contains(out, "AI: fake") || !strings.Contains(out, ": downloaded") {
		t.Errorf("%q", out)
	}
}

func TestAIStatusReportsARunningEngine(t *testing.T) {
	aiDirs(t)
	useAI(t, nil, nil)
	d := runtime.DefaultDirs()
	if err := os.MkdirAll(d.State, 0o700); err != nil {
		t.Fatal(err)
	}
	// This test binary stands in for the engine: the saved process is us.
	exe, _ := os.Executable()
	state := `{"pid":` + strconv.Itoa(os.Getpid()) + `,"port":1,"model":"/m/a.gguf","bin":"` + exe + `"}`
	if err := os.WriteFile(filepath.Join(d.State, "llama.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, out, _ := runSS("ai", "status"); !strings.Contains(out, "Local engine: running (/m/a.gguf)") {
		t.Errorf("%q", out)
	}
}

func TestAICommandsReportBrokenSettings(t *testing.T) {
	aiDirs(t)
	t.Setenv("SAFESHELL_AI", "sometimes")
	for _, args := range [][]string{{"ai", "status"}, {"ai", "setup"}, {"ai", "stop"}} {
		if code, _, e := runSS(args...); code != 1 || !strings.Contains(e, "SAFESHELL_AI") {
			t.Errorf("%v: %d %q", args, code, e)
		}
	}
}

func TestAISetupReportsAFailedDownload(t *testing.T) {
	aiDirs(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1") // nothing listens: no network is ever reached
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	if code, _, e := runSS("ai", "setup"); code != 1 || !strings.Contains(e, "safeshell ai setup:") {
		t.Errorf("%d %q", code, e)
	}
}

func TestBenchRejectsABadPause(t *testing.T) {
	aiDirs(t)
	for _, pace := range []string{"soon", "-1s"} {
		if code, _, e := runSS("ai", "bench", "--pace", pace); code != 2 || !strings.Contains(e, "--pace") {
			t.Errorf("%s: %d %q", pace, code, e)
		}
	}
}

func TestDoctorAIWithBrokenSettingsAndNoEngine(t *testing.T) {
	aiDirs(t)
	var b strings.Builder
	t.Setenv("SAFESHELL_AI", "sometimes")
	doctorAI(&b)
	if !strings.Contains(b.String(), "AI settings are broken") {
		t.Errorf("%q", b.String())
	}
	t.Setenv("SAFESHELL_AI", "")
	b.Reset()
	cfg := config.Config{}
	cfg.AI.Local.GPU = "cpu"
	if err := config.Save(config.Path(), cfg); err != nil {
		t.Fatal(err)
	}
	doctorAI(&b)
	if !strings.Contains(b.String(), "Local AI (mode auto, gpu setting cpu)") || !strings.Contains(b.String(), "none yet (run: safeshell ai setup)") {
		t.Errorf("%q", b.String())
	}
	if orNone([]string{"cpu", "vulkan"}) != "cpu, vulkan" {
		t.Error("orNone")
	}
}

func TestExplainSaysWhenTheModelFails(t *testing.T) {
	fakeAnalyze(t, sampleAnalysis())
	useAI(t, fakeAI{err: errors.New("quota used up\nsecond line")}, nil)
	code, _, e := runSS("explain", "ls")
	if code != 1 || !strings.Contains(e, "safeshell explain: quota used up") || strings.Contains(e, "second line") {
		t.Errorf("%d %q", code, e)
	}
}

func TestAIReviewPrintsFailuresButNeverBlocks(t *testing.T) {
	a := sampleAnalysis()
	var b strings.Builder
	useAI(t, nil, errors.New("settings are broken"))
	aiReview(&a, &b)
	if !strings.Contains(b.String(), "AI review skipped: settings are broken") {
		t.Errorf("%q", b.String())
	}
	b.Reset()
	useAI(t, fakeAI{err: errors.New("down")}, nil)
	aiReview(&a, &b)
	if !strings.Contains(b.String(), "AI review unavailable: down") {
		t.Errorf("%q", b.String())
	}
}

// fakeEngine installs the model and the cpu engine of the default setup under the temp XDG folders.
func fakeEngine(t *testing.T) {
	t.Helper()
	cfg := config.Config{}
	cfg.AI.Local.GPU = "cpu"
	if err := config.Save(config.Path(), cfg); err != nil {
		t.Fatal(err)
	}
	d := runtime.DefaultDirs()
	m, _ := models.Find(models.Default)
	if err := os.MkdirAll(filepath.Dir(d.ModelPath(m.File)), 0o755); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(d.ModelPath(m.File))
	_ = f.Truncate(m.Size)
	f.Close()
	bin := d.ServerBin(runtime.Candidates(hostArch, "cpu")[0])
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestAISetupWhenEverythingIsAlreadyInstalled(t *testing.T) {
	aiDirs(t)
	fakeEngine(t)
	code, out, e := runSS("ai", "setup")
	if code != 0 || !strings.Contains(out, "Ready: Qwen2.5-Coder 3B") || e != "" {
		t.Errorf("%d %q %q", code, out, e)
	}
}

func TestAIStopStopsARunningEngine(t *testing.T) {
	aiDirs(t)
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	t.Cleanup(func() { _ = sleep.Process.Kill(); _ = sleep.Wait() })
	d := runtime.DefaultDirs()
	if err := os.MkdirAll(d.State, 0o700); err != nil {
		t.Fatal(err)
	}
	state := `{"pid":` + strconv.Itoa(sleep.Process.Pid) + `,"port":1,"model":"/m/a.gguf","bin":"sleep"}`
	if err := os.WriteFile(filepath.Join(d.State, "llama.json"), []byte(state), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := runSS("ai", "stop"); code != 0 || !strings.Contains(out, "was stopped") {
		t.Errorf("%d %q", code, out)
	}
}

func TestBenchPrintsEachRequestAndTheScore(t *testing.T) {
	aiDirs(t)
	oldB, oldL := benchBackend, loadRequests
	t.Cleanup(func() { benchBackend, loadRequests = oldB, oldL })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) {
		return fakeAI{reply: `{"command":"echo hi"}`}, nil
	}
	loadRequests = eval.LoadRequests
	code, out, _ := runSS("ai", "bench", "--model", models.Default, "--pace", "0s")
	if code != 0 || !strings.Contains(out, "Testing fake on") || !strings.Contains(out, "FAIL") || !strings.Contains(out, "correct") {
		t.Errorf("%d %q", code, out)
	}
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) { return fakeAI{err: errors.New("down")}, nil }
	code, out, _ = runSS("ai", "bench")
	if code != 1 || !strings.Contains(out, "SKIP") || !strings.Contains(out, "never reached the model") {
		t.Errorf("%d %q", code, out)
	}
}

func TestBenchReportsAnUnreadableRequestList(t *testing.T) {
	aiDirs(t)
	oldB, oldL := benchBackend, loadRequests
	t.Cleanup(func() { benchBackend, loadRequests = oldB, oldL })
	benchBackend = func(config.Config, runtime.Dirs) (ai.Backend, error) { return fakeAI{}, nil }
	loadRequests = func() ([]eval.Request, error) { return nil, errors.New("broken list") }
	if code, _, e := runSS("ai", "bench"); code != 1 || !strings.Contains(e, "broken list") {
		t.Errorf("%d %q", code, e)
	}
}

func TestDoctorAIDescribesOtherMachines(t *testing.T) {
	aiDirs(t)
	oldD, oldA := nvidiaDriver, hostArch
	t.Cleanup(func() { nvidiaDriver, hostArch = oldD, oldA })
	nvidiaDriver = func() int { return 580 }
	var b strings.Builder
	doctorAI(&b)
	if !strings.Contains(b.String(), "driver 580") {
		t.Errorf("%q", b.String())
	}
	nvidiaDriver = func() int { return 0 }
	hostArch = "riscv64"
	b.Reset()
	doctorAI(&b)
	if !strings.Contains(b.String(), "none found") || !strings.Contains(b.String(), "none for riscv64: use a cloud model") {
		t.Errorf("%q", b.String())
	}
}
