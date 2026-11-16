package shellparse

import (
	"slices"
	"strings"
	"testing"
)

func TestWordsAreUnquotedLikeTheShell(t *testing.T) {
	tests := []struct {
		line     string
		name     string
		wantArgs []string
	}{
		{`r\m -rf /`, "rm", []string{"-rf", "/"}},
		{`\r\m -rf /`, "rm", []string{"-rf", "/"}},
		{`'r'm -rf /`, "rm", []string{"-rf", "/"}},
		{`"r"m -rf /`, "rm", []string{"-rf", "/"}},
		{`rm -rf \/`, "rm", []string{"-rf", "/"}},
		{`rm -rf "\/"`, "rm", []string{"-rf", `\/`}},              // inside "" only \ $ ` " newline are escapes
		{`rm -rf "a\$b\"c\\d"`, "rm", []string{"-rf", `a$b"c\d`}}, // those four are removed
		{`rm -rf '\/'`, "rm", []string{"-rf", `\/`}},              // single quotes keep everything
		{"rm -rf a\\\nb", "rm", []string{"-rf", "ab"}},            // line continuation
		{`find . -exec rm {} \;`, "rm", []string{"{}"}},
		{"cat <<EOF | sh\nrm -rf \\/\nEOF", "rm", []string{"-rf", "/"}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			c := find(mustParse(t, tt.line), tt.name)
			if c == nil {
				t.Fatalf("no %q command found", tt.name)
			}
			if !slices.Equal(c.Args, tt.wantArgs) {
				t.Errorf("args = %q, want %q", c.Args, tt.wantArgs)
			}
		})
	}
}

func TestRootAndNamespaceWrappers(t *testing.T) {
	tests := []struct {
		line string
		sudo bool
	}{
		{"su -c 'rm -rf /'", false},
		{"su - bob -c 'rm -rf /'", false},
		{"su -lc 'rm -rf /' bob", false},
		{"su -s /bin/sh bob --command='rm -rf /'", false},
		{"su --command 'rm -rf /'", false},
		{"runuser -u bob -- rm -rf /", true},
		{"runuser -u bob -g adm rm -rf /", true},
		{"runuser -l bob -c 'rm -rf /'", false},
		{"pkexec rm -rf /", true},
		{"pkexec --user bob rm -rf /", true},
		{"nsenter -t 1 -m -u rm -rf /", false},
		{"nsenter --target 1 --mount=/proc/1/ns/mnt rm -rf /", false},
		{"watch rm -rf /", false},
		{"watch -n 5 'rm -rf /'", false},
		{"watch -x rm -rf /", false},
		{"watch --exec rm -rf /", false},
		{"flock /tmp/l rm -rf /", false},
		{"flock -w 5 -x /tmp/l rm -rf /", false},
		{"flock -n /tmp/l -c 'rm -rf /'", false},
		{"flock --command='rm -rf /' /tmp/l", false},
		{"chroot / rm -rf /", false},
		{"chroot --userspec bob:bob / rm -rf /", false},
		{"sudo su -c 'rm -rf /'", false},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			c := find(mustParse(t, tt.line), "rm")
			if c == nil {
				t.Fatal("no rm command found")
			}
			if c.Sudo != tt.sudo || !slices.Equal(c.Args, []string{"-rf", "/"}) {
				t.Errorf("sudo=%v args=%q, want sudo=%v args=[-rf /]", c.Sudo, c.Args, tt.sudo)
			}
		})
	}
}

// Like `sudo sh -c`, the shell that su -c starts carries the root flag; the
// commands inside it are read as nested code.
func TestSuCodeRunsAsRoot(t *testing.T) {
	for _, line := range []string{"su -c 'rm -rf /'", "runuser -l bob -c 'rm -rf /'"} {
		if c := find(mustParse(t, line), "sh"); c == nil || !c.Sudo {
			t.Errorf("%q: the shell must run as root: %+v", line, c)
		}
	}
}

// A wrapper that names no command stays the command, so it is still rated.
func TestWrapperWithoutACommandStaysTheCommand(t *testing.T) {
	for _, line := range []string{"su", "su -", "su - bob", "su --", "su -c", "runuser -u", "runuser -u bob", "pkexec", "nsenter -t 1",
		"watch", "watch -n 5", "flock", "flock /tmp/l", "flock -c", "flock -w", "chroot /mnt", "chroot --userspec bob"} {
		r := mustParse(t, line)
		if len(r.Pipelines) != 1 || r.Pipelines[0].Cmds[0].Name != strings.Fields(line)[0] {
			t.Errorf("%q: got %v, want the wrapper as the command", line, names(r))
		}
	}
	if c := find(mustParse(t, "su bob"), "su"); c == nil || !c.Sudo {
		t.Error("su runs as root")
	}
}

func TestChrootIsKeptAndItsCommandIsFound(t *testing.T) {
	r := mustParse(t, "chroot /mnt rm -rf /")
	if got := names(r); !slices.Equal(got, []string{"rm", "chroot"}) {
		t.Errorf("names = %v, want rm and chroot", got)
	}
}

func TestEnvChdirTracksTheDirectory(t *testing.T) {
	tests := map[string]string{
		"env -C / rm -rf x":        "/",
		"env --chdir=/srv rm -x":   "/srv",
		"env -C sub rm x":          "sub",
		"cd /tmp && env -C a rm x": "/tmp/a",
		"env -iC /opt FOO=1 rm x":  "/opt",
		"env -C $D rm x":           "?",
		"cd -P /tmp && rm x":       "/tmp",
		"cd $D && cd sub && rm x":  "?",
	}
	for line, want := range tests {
		if c := find(mustParse(t, line), "rm"); c == nil || c.Cwd != want {
			t.Errorf("%q: command = %+v, want cwd %q", line, c, want)
		}
	}
}

func TestShellInputBuiltFromLiteralText(t *testing.T) {
	for _, line := range []string{
		"sh <<EOF\nrm -rf ~\nEOF",
		"bash <<< 'rm -rf ~'",
		"printf 'rm -rf ~' | sh",
		`printf '%s\n' 'rm -rf ~' | sh`,
		"echo rm -rf ~ | bash",
		"cat <<'X' | sh\nrm -rf ~\nX",
		"cat <<< 'rm -rf ~' | sh",
		"cat -u <<< 'rm -rf ~' | sh",
		"source /dev/stdin <<< 'rm -rf ~'",
		". /dev/stdin <<< 'rm -rf ~'",
		"echo 'rm -rf ~' | source /dev/stdin",
	} {
		if find(mustParse(t, line), "rm") == nil {
			t.Errorf("%q: literal text run by a shell was not analysed", line)
		}
	}
	for _, line := range []string{
		"cat <<'X' | sh -c ls\nrm -rf ~\nX", // sh -c does not read stdin
		"cat file <<'X' | sh\nrm -rf ~\nX",  // cat reads the file, not the here-document
		"source script.sh <<< 'rm -rf ~'",   // a named file
		"printf 'a %s' 'rm -rf ~' | tr a b", // not fed to a shell
	} {
		if find(mustParse(t, line), "rm") != nil {
			t.Errorf("%q: text that is not run as code was analysed", line)
		}
	}
}

func TestParallelAndStraceCommands(t *testing.T) {
	for _, c := range []struct {
		args []string
		want []string
	}{
		{[]string{"parallel", "rm", "-rf", ":::", "/", "/home"}, []string{"rm", "-rf", "/"}},
		{[]string{"parallel", "-j", "4", "rm", "{}/x", ":::", "/a"}, []string{"rm", "/a/x"}},
		{[]string{"parallel", ":::", "rm -rf /"}, []string{"sh", "-c", "rm -rf /"}},
		{[]string{"parallel", "rm", "-rf"}, []string{"rm", "-rf"}},
		{[]string{"parallel", "rm", ":::"}, nil},
		{[]string{"strace", "-f", "-o", "/tmp/t", "rm", "x"}, []string{"rm", "x"}},
		{[]string{"strace", "-p", "123"}, []string{}},
	} {
		var cmd Command
		if got := wrapped(c.args, &cmd); !slices.Equal(got, c.want) {
			t.Errorf("%q = %q, want %q", c.args, got, c.want)
		}
	}
}
