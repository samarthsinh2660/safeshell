package shellparse

import (
	"slices"
	"testing"
)

func TestRedirectOnlyWrapperKeepsRedirect(t *testing.T) {
	for _, line := range []string{"exec > /etc/passwd", "exec 3> /dev/sda", "command > /dev/sda", "ls | exec > /etc/passwd"} {
		found := false
		for _, p := range mustParse(t, line).Pipelines {
			for _, c := range p.Cmds {
				found = found || (c.Name == "" && len(c.Redirs) == 1)
			}
		}
		if !found {
			t.Errorf("%q: the redirect was dropped", line)
		}
	}
	if got := names(mustParse(t, "exec")); len(got) != 0 {
		t.Errorf("a bare exec has nothing to rate, got %v", got)
	}
}

func TestShellCClusters(t *testing.T) {
	for _, line := range []string{
		"bash -lc 'rm -rf ~'", "bash -xc 'rm -rf ~'", "sh -ec 'rm -rf ~'", "bash -c -- 'rm -rf ~'",
		`find . -exec sh -c 'rm -rf /' \;`, "find . -exec bash -c 'rm -rf ~' {} +",
	} {
		if find(mustParse(t, line), "rm") == nil {
			t.Errorf("%q: the code after -c was not analysed", line)
		}
	}
	for _, line := range []string{`echo "rm -rf /" | bash -xc`, "bash -c --", `echo "rm -rf /" | bash -lc 'ls'`} {
		if find(mustParse(t, line), "rm") != nil {
			t.Errorf("%q: text that is not run as code was analysed", line)
		}
	}
}

func TestWrapperOptionsWithValues(t *testing.T) {
	tests := []struct {
		line string
		sudo bool
		args []string
	}{
		{"exec -a ls rm -rf /", false, []string{"-rf", "/"}},
		{"sudo -iu root rm -rf /", true, []string{"-rf", "/"}},
		{"sudo -uroot rm -rf /", true, []string{"-rf", "/"}},
		{"sudo --user root rm -rf /", true, []string{"-rf", "/"}},
		{"timeout --signal KILL 5 rm -rf /", false, []string{"-rf", "/"}},
		{"stdbuf -o 0 rm -rf /", false, []string{"-rf", "/"}},
		{"xargs --max-args 1 rm -rf", false, []string{"-rf"}},
		{"xargs -n1 rm -rf", false, []string{"-rf"}},
		{"env -S 'rm -rf /'", false, []string{"-rf", "/"}},
		{"env -S'rm -rf /'", false, []string{"-rf", "/"}},
		{"env --split-string='rm -rf /'", false, []string{"-rf", "/"}},
		{"env --split-string 'FOO=1 sudo rm -rf /'", true, []string{"-rf", "/"}},
		{"env -iu HOME rm -rf /", false, []string{"-rf", "/"}},
		{"env --unset HOME rm -rf /", false, []string{"-rf", "/"}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			c := find(mustParse(t, tt.line), "rm")
			if c == nil || c.Sudo != tt.sudo || !slices.Equal(c.Args, tt.args) {
				t.Errorf("real command not found: %+v", c)
			}
		})
	}
}

func TestEnvSplitOddInput(t *testing.T) {
	for _, line := range []string{"env -S", "env -S ''", "env -S '('", "env -S '(ls)'"} {
		if got := names(mustParse(t, line)); len(got) > 1 {
			t.Errorf("%q: unexpected commands %v", line, got)
		}
	}
}
