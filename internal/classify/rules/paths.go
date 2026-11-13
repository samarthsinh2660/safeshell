package rules

import (
	"os"
	"path"
	"strings"
)

var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/boot", "/var", "/opt", "/sys", "/proc", "/dev", "/root", "/home", "/srv", "/run"}

// Norm makes common spellings of the same location comparable.
func Norm(p string) string {
	p = strings.ReplaceAll(p, "${HOME}", "~")
	if strings.HasPrefix(p, "$HOME") {
		p = "~" + strings.TrimPrefix(p, "$HOME")
	}
	if p == "" {
		return p
	}
	// Clean resolves "." and ".." so `rm -rf /tmp/..` is seen as `rm -rf /`.
	return path.Clean(p)
}

// Critical reports whether deleting or rewriting a path would damage the whole
// system or home directory, and says what the path is.
func Critical(raw string) (bool, string) {
	p := Norm(raw)
	base := strings.TrimSuffix(p, "/*")
	if base == "" {
		base = "/"
	}
	if base == "/" {
		return true, "the entire filesystem"
	}
	if base == "~" || (home() != "" && base == home()) {
		return true, "your whole home directory"
	}
	for _, d := range systemDirs {
		if base == d {
			return true, "the system directory " + d
		}
	}
	if strings.HasPrefix(base, "/home/") && strings.Count(strings.Trim(base, "/"), "/") == 1 {
		return true, "a user's whole home directory"
	}
	return false, ""
}

// UnderSystem reports whether raw is inside a system directory such as /etc or
// /usr. /home, /dev, /run and /var are left out: they hold user and runtime data.
func UnderSystem(raw string) bool {
	p := Norm(raw)
	for _, d := range systemDirs {
		if strings.HasPrefix(p, d+"/") && d != "/home" && d != "/dev" && d != "/run" && d != "/var" {
			return true
		}
	}
	return false
}

// wholeCwd reports a target that means "everything in the current directory".
func wholeCwd(p string) bool {
	switch Norm(p) {
	case ".", "*", "./*", "..", "../*", ".*", "./.*":
		return true
	}
	return false
}

// IsBlockDevice reports whether p is a disk, partition or loop device under /dev.
func IsBlockDevice(p string) bool {
	for _, pre := range []string{"/dev/sd", "/dev/hd", "/dev/vd", "/dev/xvd", "/dev/nvme", "/dev/mmcblk", "/dev/disk", "/dev/mapper", "/dev/md", "/dev/loop"} {
		if strings.HasPrefix(p, pre) {
			return true
		}
	}
	return false
}

func home() string {
	h, _ := os.UserHomeDir()
	return h
}

// Shq quotes s for display only if it needs it.
func Shq(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\!&;|<>(){}") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

var harmlessTargets = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/tty": true, "/dev/zero": true}

// WriteTarget rates writing (or appending) to the file t on behalf of the
// command line cmd. appendAlt, when set, is suggested as the safer form of a
// plain overwrite. ok is false when t is harmless to write to.
func WriteTarget(cmd, t string, appending bool, appendAlt string) (f Finding, ok bool) {
	t = Norm(t)
	switch {
	case harmlessTargets[t] || strings.HasPrefix(t, "/dev/fd/"):
		return Finding{}, false
	case IsBlockDevice(t) || t == "/dev/mem" || t == "/dev/kmem":
		return Finding{Level: R4, Command: cmd, Reason: "Writes raw data onto " + t + ", destroying what is stored there."}, true
	case strings.HasPrefix(t, "/proc/") || strings.HasPrefix(t, "/sys/"):
		return Finding{Level: R4, Command: cmd, Reason: "Writes to a live kernel control file (" + t + "), which can crash or reboot the machine."}, true
	case UnderSystem(t) && !appending:
		f = Finding{Level: R3, Command: cmd, Reason: "Overwrites the system file " + t + "."}
		return f.WithAlt("cp "+Shq(t)+" "+Shq(t)+".bak", "make a backup of the file first"), true
	case UnderSystem(t):
		return Finding{Level: R2, Command: cmd, Reason: "Appends to the system file " + t + "."}, true
	case appending:
		return Finding{Level: R1, Command: cmd, Reason: "Appends to " + t + "."}, true
	}
	f = Finding{Level: R2, Command: cmd, Reason: "Overwrites " + t + " if it already exists."}
	if appendAlt != "" {
		f = f.WithAlt(appendAlt, "appends instead of overwriting")
	}
	return f, true
}
