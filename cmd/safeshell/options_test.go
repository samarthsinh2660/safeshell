package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// tree lists every file under dir with its contents, so a test can see that
// nothing changed.
func tree(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		b.WriteString(p + "\n")
		if !d.IsDir() {
			data, _ := os.ReadFile(p)
			b.Write(data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

func TestEverySubcommandAnswersHelpWithoutActing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(home, "run"))
	t.Setenv("SAFESHELL_OFF", "")
	if code, _, _ := runSS("install"); code != 0 { // a hook that uninstall would remove
		t.Fatal("install failed")
	}
	before := tree(t, home)

	for _, cmd := range []string{"check", "analyze", "simulate", "guard", "explain", "ask", "models", "ai", "voice", "undo", "snapshots", "daemon", "install", "uninstall", "hook", "version", "doctor", "tour", "help"} {
		for _, flag := range []string{"--help", "-h"} {
			code, out, errOut := runSS(cmd, flag)
			if cmd == "guard" { // a hook runs what guard prints on stdout, so its help goes to stderr and it exits 2
				if code != 2 || out != "" || errOut == "" {
					t.Errorf("guard %s: exit %d, stdout %q, stderr %q", flag, code, out, errOut)
				}
				continue
			}
			if code != 0 || out == "" || errOut != "" {
				t.Errorf("%s %s: exit %d, stdout %q, stderr %q", cmd, flag, code, out, errOut)
			}
		}
	}
	if after := tree(t, home); after != before {
		t.Errorf("--help changed files:\n%s\n--- after ---\n%s", before, after)
	}
	if rc, _ := os.ReadFile(filepath.Join(home, ".bashrc")); !strings.Contains(string(rc), "safeshell") {
		t.Error("the hook was removed")
	}
}

func TestHelpHasTheTopicAndTheOptions(t *testing.T) {
	_, topic, _ := runSS("help", "undo")
	for _, cmd := range []string{"undo", "snapshots"} {
		if _, out, _ := runSS(cmd, "--help"); !strings.Contains(out, topic) {
			t.Errorf("%s --help lacks the help undo text:\n%s", cmd, out)
		}
	}
	for cmd, opt := range map[string]string{"ai": "bench", "install": "--ai", "voice": "--text"} {
		if _, out, _ := runSS(cmd, "--help"); !strings.Contains(out, opt) {
			t.Errorf("%s --help lacks %s:\n%s", cmd, opt, out)
		}
	}
	_, out, _ := runSS("simulate", "--help")
	if !strings.Contains(out, "--timeout 30s") || !strings.Contains(out, "never touched") || strings.Contains(out, "safeshell check") {
		t.Errorf("simulate help should be its own usage lines only:\n%s", out)
	}
}

func TestHelpOptionOnlyCountsBeforeTheCommandLine(t *testing.T) {
	for _, args := range [][]string{
		{"analyze", "--json", "--help"},
		{"simulate", "--timeout", "5s", "-h"},
		{"install", "--ai", "--help"},
		{"ai", "bench", "--help"},
	} {
		if code, out, _ := runSS(args...); code != 0 || out == "" {
			t.Errorf("%v: exit %d, stdout %q", args, code, out)
		}
	}
	if code, out, e := runSS("guard", "--cwd", "/tmp", "--help"); code != 2 || out != "" || e == "" {
		t.Errorf("guard help goes to stderr: %d %q %q", code, out, e)
	}
	for _, args := range [][]string{
		{"check", "ls", "-h"},
		{"check", "--json", "ls", "--help"},
		{"guard", "--cwd", "/tmp", "--", "ls", "-h"},
		{"analyze", "--", "ls", "-h"},
	} {
		if wantsHelp(args[0], args[1:]) {
			t.Errorf("%v: -h after the command is part of the command", args)
		}
	}
	if code, out, _ := runSS("check", "ls", "-h"); code != 0 || !strings.Contains(out, "$ ls -h") {
		t.Errorf("check ls -h should rate the command: %d %q", code, out)
	}
}

func TestBadArgumentsExitTwoWithTheNextStep(t *testing.T) {
	for _, args := range [][]string{
		{"check", "--bogus"}, {"explain", "--bogus"}, {"ask", "--bogus"},
		{"analyze", "--bogus", "ls"}, {"simulate", "--bogus", "ls"},
		{"uninstall", "now"}, {"snapshots", "x"}, {"version", "x"}, {"doctor", "x"}, {"tour", "x"},
		{"models", "x"}, {"hook"}, {"install", "--bogus"}, {"daemon", "x"}, {"ai", "x"}, {"voice", "x"}, {"undo", "a", "b"},
		{"help", "nope"}, {"undo", "--bogus"}, {"guard", "--bogus", "ls"},
		{"nope", "--help"}, {"uninstal", "--help"}, {"--version", "--help"},
	} {
		code, out, errOut := runSS(args...)
		if code != 2 || errOut == "" {
			t.Errorf("%v: exit %d, stderr %q", args, code, errOut)
		}
		if args[0] == "help" && out != "" {
			t.Errorf("help nope wrote to stdout: %q", out)
		}
	}
	_, _, e := runSS("check", "--bogus")
	if !strings.Contains(e, "unknown option --bogus") || !strings.Contains(e, "safeshell check --help") {
		t.Errorf("message: %q", e)
	}
	_, _, e = runSS("uninstall", "now")
	if !strings.Contains(e, `unexpected "now"`) || !strings.Contains(e, "safeshell uninstall --help") {
		t.Errorf("message: %q", e)
	}
}

func TestVersionStillPrintsTheRelease(t *testing.T) {
	if code, out, _ := runSS("version"); code != 0 || out != "safeshell "+version+"\n" {
		t.Errorf("%d %q", code, out)
	}
}

func TestDoubleDashEndsTheOptions(t *testing.T) {
	if code, out, _ := runSS("check", "--", "rm -rf /"); code != 4 || !strings.Contains(out, "$ rm -rf /") {
		t.Errorf("check -- : %d %q", code, out)
	}
	if code, out, _ := runSS("check", "--json", "--", "ls"); code != 0 || !strings.Contains(out, `"level": "R0"`) {
		t.Errorf("check --json -- : %d %q", code, out)
	}
	var spec string
	old := simulate
	t.Cleanup(func() { simulate = old })
	simulate = func(_ context.Context, s sandbox.Spec) (sandbox.Result, error) {
		spec = s.Line + "|" + s.Cwd
		return sandbox.Result{}, nil
	}
	if code, _, _ := runSS("simulate", "--cwd", "/tmp", "--", "ls"); code != 0 || spec != "ls|/tmp" {
		t.Errorf("simulate -- : %d %q", code, spec)
	}
}
