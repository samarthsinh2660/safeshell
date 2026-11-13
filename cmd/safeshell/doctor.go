package main

import (
	"fmt"
	"io"
	"os"
	goruntime "runtime"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/models"
	"github.com/samarthsinh2660/safeshell/internal/platform"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// runDoctor is `safeshell doctor`: the sandbox, then the local AI, then voice.
func runDoctor(stdout io.Writer) int {
	code := doctor(stdout, probe())
	doctorAI(stdout)
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
