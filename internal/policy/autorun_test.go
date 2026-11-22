package policy

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func verdictOf(cmds ...string) domain.Verdict {
	var v domain.Verdict
	for _, c := range cmds {
		v.Findings = append(v.Findings, domain.Finding{Level: domain.R1, Command: c})
	}
	return v
}

// lineOf is a plain line made of the verdict's commands, as the person would type them.
func lineOf(v domain.Verdict) string {
	var cs []string
	for _, f := range v.Findings {
		cs = append(cs, f.Command)
	}
	return strings.Join(cs, " | ")
}

// The raw line is checked too, because the classifier drops assignments, env,
// redirects and here-strings before it reports a command. These lines went
// through the real classifier in review and each came out R0 or R1.
func TestAutorunRefusesWhatTheClassifierDrops(t *testing.T) {
	const cwd, home = "/home/u/work", "/home/u"
	for line, want := range map[string]bool{
		"ls -la":                       true,
		"mv notes.txt backup/":         true,
		"cd projects":                  true,
		"cd build && ls | grep x":      true,
		`cat <<< "$(rm -rf ~/x)"`:      false,
		`grep x <<< $(python3 -c 1)`:   false,
		"LD_PRELOAD=./x.so ls":         false,
		"env LD_PRELOAD=./x.so ls":     false,
		"BASH_ENV=x cat y":             false,
		"sudo ls":                      false,
		"cat x >> ~/.ssh/config":       false,
		"cp x ~/.bashrc":               false,
		"cp x .git/hooks/pre-commit":   false,
		"cp x .envrc":                  false,
		"mv x ~/.config/autostart/":    false,
		"mkdir -p ~/.config/autostart": false,
		"cp -t ~/.config/autostart x":  false,
		"mv * /tmp":                    false,
		"file -C -m x":                 false,
		"ls $(make)":                   false,
		"ls `make`":                    false,
		"ls | sh":                      false,
		"cp a b; make":                 false,
	} {
		v := classify.Classify(line, cwd)
		if got := AutorunSafe(v, line, cwd, home); got != want {
			t.Errorf("%q: got %v, want %v", line, got, want)
		}
	}
}

func TestAutorunSafe(t *testing.T) {
	const cwd, home = "/home/u/work", "/home/u"
	unknown := verdictOf("ls")
	unknown.Unclassified = []string{"mytool"}
	for _, c := range []struct {
		name string
		v    domain.Verdict
		want bool
	}{
		{"a listing", verdictOf("ls -la"), true},
		{"open a folder", verdictOf("cd projects"), true},
		{"read with a pipe", verdictOf("cat notes.txt", "grep -r foo ."), true},
		{"move a file in the folder", verdictOf("mv notes.txt backup/"), true},
		{"move between folders at home", verdictOf("mv ~/Downloads/a.pdf ~/Documents/"), true},
		{"absolute path at home", verdictOf("cp -r /home/u/work/a /home/u/Documents/a"), true},
		{"a path above the folder but inside home", verdictOf("cp a.txt ../Documents/"), true},
		{"make and touch", verdictOf("mkdir -p notes/2026", "touch notes/2026/a.txt"), true},
		{"a glob", verdictOf("mv *.txt backup/"), true},

		{"nothing found", domain.Verdict{}, false},
		{"a program the rules do not know", unknown, false},
		{"a script", verdictOf("./run.sh"), false},
		{"an interpreter", verdictOf("python3 x.py"), false},
		{"a build tool", verdictOf("make"), false},
		{"a package manager", verdictOf("npm install"), false},
		{"a write through an option", verdictOf("sort -o ~/x y"), false},
		{"file -C writes a file", verdictOf("file -C"), false},
		{"git can run a hook or pager", verdictOf("git -c core.pager=x log"), false},
		{"a variable in front", verdictOf("LD_PRELOAD=/tmp/x.so ls"), false},
		{"sudo in front", verdictOf("sudo ls"), false},
		{"a remote command", verdictOf("ssh prod-db"), false},
		{"one bad part spoils the line", verdictOf("cd build", "make"), false},
		{"a builtin that ends the shell", verdictOf("exit"), false},
		{"an empty command", verdictOf(" "), false},

		{"overwrite the shell startup file", verdictOf("mv notes.txt ~/.bashrc"), false},
		{"overwrite ssh keys", verdictOf("cp evil ~/.ssh/authorized_keys"), false},
		{"a hidden folder by relative path", verdictOf("cp evil .ssh/authorized_keys"), false},
		{"a hidden folder in the middle", verdictOf("mv a ~/Documents/.hidden/b"), false},
		{"a hidden glob", verdictOf("mv .* backup/"), false},
		{"a path outside home", verdictOf("mv a /etc/hosts"), false},
		{"a path out through ..", verdictOf("mv a ../../etc/x"), false},
		{"home itself is not a place to put a file", verdictOf("cp a ~"), false},
		{"another user's home", verdictOf("cp a ~root/x"), false},
		{"a target-directory option", verdictOf("cp -t ~/.ssh evil"), false},
		{"force", verdictOf("mv -f a b"), false},
		{"a variable in a path", verdictOf("mv a $HOME/.bashrc"), false},
		{"a quoted path", verdictOf(`mv a "b c"`), false},
		{"a cd with a writer", verdictOf("cd ~/.ssh", "cp evil authorized_keys"), false},
	} {
		if got := AutorunSafe(c.v, lineOf(c.v), cwd, home); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestAutorunWritersNeedAKnownHomeAndFolder(t *testing.T) {
	v := verdictOf("mv a b")
	for _, c := range []struct{ cwd, home string }{{"/home/u/work", ""}, {"", "/home/u"}, {"work", "/home/u"}, {"/home/u/work", "u"}, {"/etc", "/home/u"}, {"/home/u/.ssh", "/home/u"}} {
		if AutorunSafe(v, "mv a b", c.cwd, c.home) {
			t.Errorf("cwd %q home %q must not be safe for a writer", c.cwd, c.home)
		}
	}
	if !AutorunSafe(verdictOf("ls"), "ls", "", "") {
		t.Error("a reader needs neither a folder nor a home")
	}
}
