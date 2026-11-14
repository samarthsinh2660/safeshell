package main

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/platform"
)

func TestDoctor(t *testing.T) {
	old := probe
	defer func() { probe = old }()

	probe = func() platform.Report {
		return platform.Report{Kernel: "6.8.0", UserNS: true, Overlay: true, Seccomp: true, Landlock: true, CgroupV2: true, RootFS: "ext4", Shells: []string{"bash", "zsh"}}
	}
	code, out, _ := runSS("doctor")
	if code != 0 || !strings.Contains(out, "available") || !strings.Contains(out, "copy (root is ext4)") || !strings.Contains(out, "bash, zsh") {
		t.Errorf("healthy doctor: exit %d\n%s", code, out)
	}

	probe = func() platform.Report {
		return platform.Report{Kernel: "6.8.0", Overlay: true, AppArmorRestrictsUserNS: true, UserNSErr: "fork/exec: operation not permitted"}
	}
	code, out, _ = runSS("doctor")
	if code != 1 || !strings.Contains(out, "NOT available") || !strings.Contains(out, "MISSING  user namespaces") || !strings.Contains(out, "install-apparmor.sh") {
		t.Errorf("blocked doctor: exit %d\n%s", code, out)
	}
}
