package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestEverySubcommandIsDocumented keeps the usage text, the help topic and the man page in step with run().
func TestEverySubcommandIsDocumented(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.SplitN(string(src), "func run(", 2)[1]
	body = strings.SplitN(body, "type palette", 2)[0]
	man, err := os.ReadFile("../../debian/safeshell.1")
	if err != nil {
		t.Fatal(err)
	}
	var commands string
	for _, tp := range helpTopics {
		if tp.name == "commands" {
			commands = tp.body
		}
	}
	n := 0
	for _, m := range regexp.MustCompile(`(?m)^\tcase ("[a-z-]+"(?:, "[a-z-]+")*):`).FindAllStringSubmatch(body, -1) {
		for _, name := range strings.Split(strings.ReplaceAll(m[1], `"`, ""), ", ") {
			if strings.HasPrefix(name, "-") {
				continue
			}
			n++
			if !strings.Contains(usage, "safeshell "+name) {
				t.Errorf("%s is missing from the usage text", name)
			}
			if !strings.Contains(commands, name) {
				t.Errorf("%s is missing from the help commands topic", name)
			}
			if !regexp.MustCompile(`(?m)^\.(B|BI|RB) .*\b` + name + `\b`).Match(man) {
				t.Errorf("%s is missing from the man page", name)
			}
		}
	}
	if n < 20 {
		t.Errorf("found only %d subcommands in run(); the pattern is out of date", n)
	}
}

func TestVersionIsPrintedWithoutALeadingV(t *testing.T) {
	old := version
	defer func() { version = old }()
	for _, v := range []string{"v1.2.3", "1.2.3"} {
		version = v
		if code, out, _ := runSS("version"); code != 0 || out != "safeshell 1.2.3\n" {
			t.Errorf("%s: %d %q", v, code, out)
		}
	}
}

func TestUninstallKnowsAnyValueOfSafeshellOffMeansPaused(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("SAFESHELL_OFF", "0")
	if code, _, _ := runSS("install"); code != 0 {
		t.Fatal("install failed")
	}
	if _, out, _ := runSS("uninstall"); strings.Contains(out, "This terminal still has it") {
		t.Errorf("%q", out)
	}
}
