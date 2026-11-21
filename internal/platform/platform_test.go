package platform

import (
	"slices"
	"strings"
	"testing"
)

func TestParseLSM(t *testing.T) {
	got := parseLSM("lockdown,capability,landlock,yama,apparmor,bpf\n")
	if !slices.Equal(got, []string{"lockdown", "capability", "landlock", "yama", "apparmor", "bpf"}) {
		t.Errorf("got %v", got)
	}
	if parseLSM("  \n") != nil {
		t.Error("blank should be nil")
	}
	if !has(got, "landlock") || has(got, "selinux") {
		t.Error("has() wrong")
	}
}

func TestRootFSType(t *testing.T) {
	mi := `22 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw
23 22 0:5 / /dev rw,nosuid shared:2 - devtmpfs udev rw
24 22 8:3 / /home rw shared:3 - btrfs /dev/sda3 rw`
	if got := rootFSType(mi); got != "ext4" {
		t.Errorf("got %q, want ext4", got)
	}
	over := mi + "\n40 22 0:30 / / rw - overlay overlay rw"
	if got := rootFSType(over); got != "overlay" {
		t.Errorf("the last mount on / wins, got %q", got)
	}
	if rootFSType("garbage\n\n") != "" {
		t.Error("no match should be empty")
	}
}

func TestHints(t *testing.T) {
	full := Report{UserNS: true, Overlay: true, Landlock: true, Seccomp: true, CgroupV2: true}
	if h := full.Hints(); len(h) != 1 || !strings.Contains(h[0], "Everything needed") {
		t.Errorf("healthy machine: %v", h)
	}
	if !full.CanSimulate() {
		t.Error("healthy machine should be able to simulate")
	}

	apparmor := Report{Overlay: true, AppArmorRestrictsUserNS: true, Seccomp: true, Landlock: true, CgroupV2: true}
	h := apparmor.Hints()
	if apparmor.CanSimulate() || len(h) != 1 || !strings.Contains(h[0], "install-apparmor.sh") {
		t.Errorf("apparmor case: %v", h)
	}

	other := Report{Overlay: true, UserNSErr: "operation not permitted", Seccomp: true, Landlock: true, CgroupV2: true}
	if h := other.Hints(); len(h) != 1 || !strings.Contains(h[0], "operation not permitted") {
		t.Errorf("generic userns failure should include the error: %v", h)
	}

	none := Report{}
	if len(none.Hints()) != 5 || none.CanSimulate() {
		t.Errorf("empty report should list every problem: %v", none.Hints())
	}
}

func TestProbeDoesNotPanicAndIsConsistent(t *testing.T) {
	r := Probe()
	if r.Kernel == "" {
		t.Skip("no /proc on this system")
	}
	if r.UserNS && r.UserNSErr != "" {
		t.Errorf("success must not carry an error: %+v", r)
	}
	if !r.UserNS && r.UserNSErr == "" {
		t.Errorf("failure must say why: %+v", r)
	}
}
