package rules

import (
	"slices"
	"testing"
)

func TestNorm(t *testing.T) {
	tests := map[string]string{
		"/tmp/..":       "/",
		"/./":           "/",
		"/etc//":        "/etc",
		"$HOME/x":       "~/x",
		"${HOME}/x":     "~/x",
		"$HOME":         "~",
		"./*":           "*",
		"a/b/../c":      "a/c",
		"":              "",
		"/usr/lib/../.": "/usr",
	}
	for in, want := range tests {
		if got := Norm(in); got != want {
			t.Errorf("Norm(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCritical(t *testing.T) {
	yes := []string{"/", "/*", "~", "~/", "~/*", "$HOME", "$HOME/*", "/etc", "/etc/*", "/usr/", "/home", "/home/*", "/home/alice", "/var", "/boot", "/tmp/.."}
	no := []string{"/tmp", "/tmp/x", "~/projects", "/home/alice/projects", "/etc/nginx/nginx.conf", "build", ".", "/usr/local/bin/tool", "/var/log/app.log"}
	for _, p := range yes {
		if ok, _ := Critical(p); !ok {
			t.Errorf("Critical(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if ok, what := Critical(p); ok {
			t.Errorf("Critical(%q) = true (%s), want false", p, what)
		}
	}
}

func TestUnderSystem(t *testing.T) {
	for p, want := range map[string]bool{
		"/etc/hosts": true, "/usr/bin/x": true, "/boot/grub/grub.cfg": true, "/etc": false,
		"/tmp/x": false, "/home/a/x": false, "/var/log/x": false, "/dev/sda": false, "notes.txt": false,
	} {
		if got := UnderSystem(p); got != want {
			t.Errorf("UnderSystem(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestIsBlockDevice(t *testing.T) {
	for p, want := range map[string]bool{
		"/dev/sda": true, "/dev/sda1": true, "/dev/nvme0n1p2": true, "/dev/mapper/root": true, "/dev/mmcblk0": true,
		"/dev/null": false, "/dev/tty": false, "/dev/zero": false, "/tmp/sda": false,
	} {
		if got := IsBlockDevice(p); got != want {
			t.Errorf("IsBlockDevice(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestWholeCwd(t *testing.T) {
	for p, want := range map[string]bool{".": true, "*": true, "./*": true, "..": true, ".*": true, "build": false, "src/*": false, "/": false} {
		if got := wholeCwd(p); got != want {
			t.Errorf("wholeCwd(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestShq(t *testing.T) {
	for in, want := range map[string]string{
		"plain": "plain", "~/x": "~/x", "a b": "'a b'", "it's": `'it'\''s'`, "$x": "'$x'", "": "''", "a;b": "'a;b'",
	} {
		if got := Shq(in); got != want {
			t.Errorf("Shq(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseFlags(t *testing.T) {
	f := parseFlags([]string{"-rf", "--force", "--signal=9", "-", "a", "--", "-notflag"})
	if !f.has('r', "") || !f.has('f', "") || !f.has(0, "force") {
		t.Errorf("short/long flags not read: %+v", f)
	}
	if f.long["signal"] != "9" {
		t.Errorf("long value not read: %+v", f.long)
	}
	if want := []string{"-", "a", "-notflag"}; !slices.Equal(f.pos, want) {
		t.Errorf("operands = %v, want %v (a lone - and everything after -- are operands)", f.pos, want)
	}
	if f.sub() != "-" {
		t.Errorf("sub() = %q", f.sub())
	}
	if parseFlags(nil).sub() != "" {
		t.Error("sub() on no args should be empty")
	}
}

func TestEveryRuleTableEntryIsNonNil(t *testing.T) {
	for name, r := range table {
		if r == nil {
			t.Errorf("rule %q is nil", name)
		}
	}
}

func TestSkipGlobals(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"git", []string{"-C", "/repo", "status"}, []string{"status"}},
		{"git", []string{"--no-pager", "-c", "a=b", "log"}, []string{"log"}},
		{"git", []string{"--git-dir=x", "status"}, []string{"status"}},
		{"git", []string{"-C"}, []string{}},
		{"docker", []string{"-H", "tcp://x", "ps"}, []string{"ps"}},
		{"kubectl", []string{"-n", "prod", "get", "pods"}, []string{"get", "pods"}},
		{"git", nil, nil},
		{"rm", []string{"-rf", "x"}, []string{"-rf", "x"}},
	}
	for _, tc := range tests {
		if got := skipGlobals(tc.name, tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("skipGlobals(%s, %v) = %v, want %v", tc.name, tc.args, got, tc.want)
		}
	}
}
