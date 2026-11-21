// Package platform detects what this machine lets SafeShell do (user
// namespaces, OverlayFS, AppArmor restrictions, seccomp, Landlock, snapshots)
// and turns the answers into plain advice. It powers `safeshell doctor` and lets
// tests skip when a capability is missing.
package platform

import (
	"os/exec"
	"strings"
)

// Report is a snapshot of the machine's capabilities.
type Report struct {
	Kernel string
	// UserNS is true if this process could create a user+mount namespace and
	// mount a filesystem inside it.
	// It depends on the program asking: AppArmor can allow `bwrap` and deny `safeshell`.
	UserNS    bool
	UserNSErr string
	// AppArmorRestrictsUserNS is the kernel.apparmor_restrict_unprivileged_userns sysctl.
	AppArmorRestrictsUserNS bool
	Overlay                 bool // the kernel offers the overlay filesystem
	Landlock                bool
	Seccomp                 bool
	CgroupV2                bool
	RootFS                  string // filesystem type of "/", decides the snapshot backend
	Bwrap                   string // path to bubblewrap, or ""
	Shells                  []string
	LSMs                    []string
}

// CanSimulate reports whether the OverlayFS sandbox can run here.
func (r Report) CanSimulate() bool { return r.UserNS && r.Overlay }

// Hints turns the report into advice, most important first.
func (r Report) Hints() []string {
	var h []string
	if !r.Overlay {
		h = append(h, "The kernel has no overlay filesystem: try `sudo modprobe overlay`. Without it SafeShell cannot simulate commands.")
	}
	if !r.UserNS {
		if r.AppArmorRestrictsUserNS {
			h = append(h, "AppArmor blocks user namespaces for this program (Ubuntu 24.04+). Install a profile for safeshell with scripts/install-apparmor.sh, which needs sudo once.")
		} else {
			h = append(h, "This program cannot create user namespaces"+suffix(r.UserNSErr)+". Check the kernel setting user.max_user_namespaces and your container or VM limits.")
		}
	}
	if !r.Seccomp {
		h = append(h, "seccomp is not available; syscall-level observation of network and process effects will be limited.")
	}
	if !r.Landlock {
		h = append(h, "Landlock is not enabled; the extra filesystem hard limit is skipped.")
	}
	if !r.CgroupV2 {
		h = append(h, "cgroup v2 is not mounted; CPU and memory limits for the sandbox will be skipped.")
	}
	if r.CanSimulate() && len(h) == 0 {
		h = append(h, "Everything needed for sandbox simulation is available.")
	}
	return h
}

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " (" + s + ")"
}

// parseLSM splits the contents of /sys/kernel/security/lsm.
func parseLSM(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// rootFSType finds the filesystem type of "/" in /proc/self/mountinfo text.
// Lines look like: 29 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw
func rootFSType(mountinfo string) string {
	fstype := ""
	for _, line := range strings.Split(mountinfo, "\n") {
		before, after, ok := strings.Cut(line, " - ")
		if !ok {
			continue
		}
		fields := strings.Fields(before)
		if len(fields) < 5 || fields[4] != "/" {
			continue
		}
		if f := strings.Fields(after); len(f) > 0 {
			fstype = f[0] // the last "/" mount wins (overmounts)
		}
	}
	return fstype
}

func lookPath(names ...string) []string {
	var found []string
	for _, n := range names {
		if _, err := exec.LookPath(n); err == nil {
			found = append(found, n)
		}
	}
	return found
}
