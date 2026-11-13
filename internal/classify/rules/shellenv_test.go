package rules

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func TestRuleExport(t *testing.T) {
	tests := []struct {
		args []string
		want domain.Level
	}{
		{[]string{"LD_PRELOAD=/tmp/x.so"}, R2},
		{[]string{"LD_LIBRARY_PATH=/tmp/lib"}, R2},
		{[]string{"LD_LIBRARY_PATH=/opt/lib"}, R0},
		{[]string{"LD_LIBRARY_PATH=/usr/local/cuda/lib64:$LD_LIBRARY_PATH"}, R0},
		{[]string{"LD_LIBRARY_PATH+=:/tmp/lib"}, R0},
		{[]string{"LD_PRELOAD+=:/tmp/x.so"}, R2},
		{[]string{"LD_PRELOAD="}, R0},
		{[]string{"LD_PRELOAD"}, R0},
		{[]string{"PATH+=:/opt/bin"}, R0},
		{[]string{"PATH+=./bin"}, R0},
		{[]string{"PROMPT_COMMAND=history -a"}, R2},
		{[]string{"PROMPT_COMMAND+=;date"}, R2},
		{[]string{"PATH=/tmp/x:$PATH"}, R2},
		{[]string{"PATH=/var/tmp:$PATH"}, R2},
		{[]string{"PATH=/dev/shm/bin:$PATH"}, R2},
		{[]string{"PATH=.:$PATH"}, R2},
		{[]string{"PATH=bin:$PATH"}, R2},
		{[]string{"PATH=:$PATH"}, R2},
		{[]string{"-x", "A=1", "PATH=/usr/bin"}, R0},
		{[]string{"PATH=$HOME/bin:$PATH"}, R0},
		{[]string{"PATH=~/bin:$PATH"}, R0},
		{[]string{"PATH=/usr/local/bin:$PATH"}, R0},
		{[]string{"PATH=$PATH:/tmp/x"}, R0},
		{[]string{"PATH=/tmpfiles:$PATH"}, R0},
		{[]string{"PATH"}, R0},
		{[]string{"EDITOR=vim", "LANG=C"}, R0},
		{nil, R0},
	}
	for _, tc := range tests {
		c := shellparse.Command{Name: "export", Args: tc.args}
		fs := ruleExport(c, flags{})
		got := R0
		for _, f := range fs {
			got = max(got, f.Level)
		}
		if got != tc.want || len(fs) == 0 {
			t.Errorf("export %v = %s (%d findings), want %s", tc.args, got, len(fs), tc.want)
		}
	}
}

func TestWriteTargetStartupFiles(t *testing.T) {
	tests := []struct {
		target    string
		appending bool
		want      domain.Level
	}{
		{"~/.bashrc", true, R2},
		{"$HOME/.zshrc", true, R2},
		{"/home/alice/.profile", true, R2},
		{"~/.config/fish/config.fish", true, R2},
		{"~/.bash_profile", false, R2},
		{"/etc/profile.d/x.sh", true, R2},
		{"~/.bashrc.bak", true, R1},
		{"~/notes.txt", true, R1},
	}
	for _, tc := range tests {
		f, ok := WriteTarget("cmd", tc.target, tc.appending, "")
		if !ok || f.Level != tc.want {
			t.Errorf("WriteTarget(%q, append=%v) = %s, want %s", tc.target, tc.appending, f.Level, tc.want)
		}
	}
}
