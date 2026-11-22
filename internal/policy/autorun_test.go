package policy

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func verdictOf(cmds ...string) domain.Verdict {
	var v domain.Verdict
	for _, c := range cmds {
		v.Findings = append(v.Findings, domain.Finding{Level: domain.R1, Command: c})
	}
	return v
}

func TestAutorunSafe(t *testing.T) {
	unknown := verdictOf("ls")
	unknown.Unclassified = []string{"mytool"}
	for _, c := range []struct {
		name string
		v    domain.Verdict
		want bool
	}{
		{"a listing", verdictOf("ls -la"), true},
		{"move a file", verdictOf("mv notes.txt backup/"), true},
		{"open a folder", verdictOf("cd projects"), true},
		{"every part is on the list", verdictOf("cd build", "ls", "grep -r foo ."), true},
		{"nothing found", domain.Verdict{}, false},
		{"a program the rules do not know", unknown, false},
		{"a script", verdictOf("./run.sh"), false},
		{"an interpreter", verdictOf("python3 x.py"), false},
		{"a build tool", verdictOf("make"), false},
		{"a package manager", verdictOf("npm install"), false},
		{"a write through an option", verdictOf("sort -o ~/.bashrc x"), false},
		{"git can run a hook or pager", verdictOf("git -c core.pager=x log"), false},
		{"a variable in front", verdictOf("LD_PRELOAD=/tmp/x.so ls"), false},
		{"sudo in front", verdictOf("sudo ls"), false},
		{"a remote command", verdictOf("ssh prod-db"), false},
		{"one bad part spoils the line", verdictOf("cd build", "make"), false},
		{"a builtin that ends the shell", verdictOf("exit"), false},
	} {
		if got := AutorunSafe(c.v); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
