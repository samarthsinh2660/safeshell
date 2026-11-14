package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/ai/local"
	"github.com/samarthsinh2660/safeshell/internal/ai/providers"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/install"
	"github.com/samarthsinh2660/safeshell/internal/ipc"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

// runDoctor is `safeshell doctor`: the sandbox, this setup (hook, daemon,
// snapshots), the AI, then voice. It only reads; it changes nothing.
func runDoctor(stdout io.Writer) int {
	code := doctor(stdout, probe())
	doctorSetup(stdout)
	doctorAI(stdout)
	doctorCloud(stdout)
	cards, _ := os.ReadFile("/proc/asound/cards")
	doctorVoice(stdout, voiceSetup(), realRunner, string(cards))
	return code
}

func doctor(w io.Writer, r platform.Report) int {
	row := func(name string, ok bool, detail string) {
		mark := "ok     "
		if !ok {
			mark = "MISSING"
		}
		fmt.Fprintf(w, "  %s  %-26s %s\n", mark, name, detail)
	}
	fmt.Fprintf(w, "SafeShell doctor (kernel %s)\n\n", r.Kernel)
	row("user namespaces", r.UserNS, r.UserNSErr)
	row("overlay filesystem", r.Overlay, "")
	row("seccomp", r.Seccomp, "")
	row("landlock", r.Landlock, "")
	row("cgroup v2", r.CgroupV2, "")
	fmt.Fprintf(w, "  info     %-26s %s\n", "snapshot backend", r.SnapshotBackend()+" (root is "+r.RootFS+")")
	fmt.Fprintf(w, "  info     %-26s %s\n", "shells found", strings.Join(r.Shells, ", "))
	fmt.Fprintf(w, "\nSandbox simulation: %v\n", map[bool]string{true: "available", false: "NOT available"}[r.CanSimulate()])
	for _, h := range r.Hints() {
		fmt.Fprintf(w, "  - %s\n", h)
	}
	if r.CanSimulate() {
		return 0
	}
	return 1
}

// doctorRow prints one check. mark is "ok", "info" or "MISSING"; a MISSING row
// says what is wrong and the exact next step.
func doctorRow(w io.Writer, mark, name, detail string) {
	fmt.Fprintf(w, "  %-7s  %-26s %s\n", mark, name, detail)
}

// doctorSetup checks the parts around the sandbox: the hook, the daemon and the snapshot store.
func doctorSetup(w io.Writer) {
	fmt.Fprintln(w, "\nSetup")
	rc := "your shell startup file"
	installed := false
	sh, err := shellFor()
	if err == nil {
		rc, installed = sh.RC, install.Installed(sh)
	}
	switch {
	case err != nil:
		doctorRow(w, "MISSING", "hook installed", err.Error()+". Switch to bash or zsh to use SafeShell")
	case installed:
		doctorRow(w, "ok", "hook installed", "in "+rc)
	default:
		doctorRow(w, "MISSING", "hook installed", "not in "+rc+". Run: safeshell install")
	}
	switch {
	case os.Getenv("SAFESHELL_OFF") != "":
		doctorRow(w, "MISSING", "hook active here", "not active: SAFESHELL_OFF is set in this terminal. Run: unset SAFESHELL_OFF")
	case paused():
		doctorRow(w, "MISSING", "hook active here", "paused: run safeshell activate")
	case os.Getenv("SAFESHELL_HOOK") == strconv.Itoa(os.Getppid()): // the hook exports its shell's pid, so a child shell that inherited it does not count
		doctorRow(w, "ok", "hook active here", "this terminal checks every command")
	case installed:
		doctorRow(w, "MISSING", "hook active here", "not active in this terminal. Open a new terminal, or run: source "+rc)
	case err != nil:
		doctorRow(w, "MISSING", "hook active here", "not active in this terminal. Switch to bash or zsh to use SafeShell")
	default:
		doctorRow(w, "MISSING", "hook active here", "not active in this terminal. Run  safeshell install  and open a new terminal")
	}
	doctorDaemon(w)
	ms, err := snapshot.List(snapshotDir())
	if err != nil {
		doctorRow(w, "MISSING", "snapshots", "cannot read "+snapshotDir()+": "+err.Error()+". Check that folder's permissions")
		return
	}
	var size int64
	for _, m := range ms {
		size += m.Bytes
	}
	doctorRow(w, "info", "snapshots", fmt.Sprintf("%d saved, %s (list them: safeshell snapshots)", len(ms), report.HumanBytes(size)))
}

// daemonWait is how long doctor waits for the daemon; tests may shorten it.
var daemonWait = 500 * time.Millisecond

// doctorDaemon says whether the optional background daemon answers.
func doctorDaemon(w io.Writer) {
	path := socketPath()
	if path == "" {
		doctorRow(w, "info", "daemon", "unavailable: XDG_RUNTIME_DIR is not set to a full path. Commands are still checked directly, just a little slower")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), daemonWait)
	defer cancel()
	err := ipc.Call(ctx, path, "ping", nil, nil)
	switch {
	case err == nil:
		doctorRow(w, "ok", "daemon", "running (stop it: safeshell daemon stop)")
	case errors.Is(err, ipc.ErrUntrusted):
		doctorRow(w, "MISSING", "daemon", path+" is not your SafeShell daemon, so it is ignored. Delete that file")
	default:
		doctorRow(w, "info", "daemon", "not running (optional). For faster checks run: safeshell daemon")
	}
}

// doctorCloud says which AI is active now and the state of the cloud provider and its key.
// It never prints the key.
func doctorCloud(w io.Writer) {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		return // doctorAI already said the settings are broken
	}
	fmt.Fprintln(w, "\nAI in use")
	b, err := backend()
	cloudKey := false
	if p, ok := providers.Find(c.AI.Cloud.Provider); ok {
		k, _ := config.Key(p)
		cloudKey = k != ""
	}
	m, _ := models.Find(c.AI.Local.Model)
	localReady := local.Llama{Dirs: runtime.DefaultDirs(), Model: m, GPU: c.AI.Local.GPU}.Ready()
	switch {
	case err != nil:
		doctorRow(w, "MISSING", "AI now", err.Error())
		if c.AI.Mode == ai.Cloud {
			return // that is the missing key; do not say it twice
		}
	case b != nil && (c.AI.Mode == ai.Local || c.AI.Mode == ai.Hybrid) && !localReady && !(c.AI.Mode == ai.Hybrid && cloudKey):
		doctorRow(w, "MISSING", "AI now", "mode "+string(c.AI.Mode)+" but the local model or engine is not installed. Run: safeshell ai setup")
	case b == nil:
		doctorRow(w, "info", "AI now", "off (mode "+string(c.AI.Mode)+"). "+aiOffHint)
	default:
		doctorRow(w, "ok", "AI now", report.Printable(b.Name())+" (mode "+string(c.AI.Mode)+")")
	}
	p, ok := providers.Find(c.AI.Cloud.Provider)
	if !ok {
		doctorRow(w, "info", "cloud provider", "none chosen. To use Claude, GPT or Gemini type /models")
		return
	}
	model := c.AI.Cloud.Model
	if model == "" {
		model = p.DefaultModel
	}
	doctorRow(w, "info", "cloud provider", p.Title+", model "+report.Printable(model))
	key, err := config.Key(p)
	if err != nil {
		doctorRow(w, "MISSING", "cloud key", err.Error())
		return
	}
	if key == "" {
		doctorRow(w, "MISSING", "cloud key", "no key for "+p.Title+". Type /models to add one, or set "+p.Env[0])
		return
	}
	doctorRow(w, "ok", "cloud key", "set for "+p.Title)
}

// nvidiaDriver and hostArch are test seams: tests replace them to describe
// other machines.
var (
	nvidiaDriver = runtime.NvidiaDriver
	hostArch     = goruntime.GOARCH
)

// doctorAI says which engine the local AI would use on this machine and what is installed.
func doctorAI(w io.Writer) {
	c, err := config.LoadEffective(config.Path())
	if err != nil {
		fmt.Fprintln(w, "\nAI settings are broken:", err)
		return
	}
	d := runtime.DefaultDirs()
	fmt.Fprintf(w, "\nLocal AI (mode %s, gpu setting %s)\n", c.AI.Mode, c.AI.Local.GPU)
	if drv := nvidiaDriver(); drv > 0 {
		fmt.Fprintf(w, "  info     %-26s driver %d\n", "NVIDIA graphics card", drv)
	} else {
		fmt.Fprintf(w, "  info     %-26s none found\n", "NVIDIA graphics card")
	}
	fmt.Fprintf(w, "  info     %-26s %v\n", "Vulkan loader", runtime.HasVulkan())
	var order, have []string
	for _, b := range runtime.Candidates(hostArch, c.AI.Local.GPU) {
		order = append(order, b.Variant)
		if d.EngineInstalled(b) {
			have = append(have, b.Variant)
		}
	}
	if len(order) == 0 {
		fmt.Fprintf(w, "  MISSING  %-26s none for %s: use a cloud model\n", "local engine", hostArch)
		return
	}
	fmt.Fprintf(w, "  info     %-26s %s (best first)\n", "engines it would try", strings.Join(order, ", "))
	fmt.Fprintf(w, "  info     %-26s %s\n", "engines installed", orNone(have))
	md, _ := models.Find(c.AI.Local.Model)
	fmt.Fprintf(w, "  info     %-26s %s, downloaded: %v\n", "model", md.Title, d.ModelInstalled(md))
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none yet (run: safeshell ai setup)"
	}
	return strings.Join(s, ", ")
}
