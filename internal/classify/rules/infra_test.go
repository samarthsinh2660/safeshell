package rules

import (
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func TestDockerManagementReadsAreR0(t *testing.T) {
	for _, line := range []string{"container ls", "image inspect x", "compose ps", "compose -p web ps"} {
		args := strings.Fields(line)
		if fs := ruleDocker(shellparse.Command{Name: "docker", Args: args}, parseFlags(args)); len(fs) != 1 || fs[0].Level != R0 {
			t.Errorf("%v: %+v", args, fs)
		}
	}
	for _, line := range []string{"container rm x", "compose -p config down -v", "compose config -o out.yml"} {
		args := strings.Fields(line)
		if fs := ruleDocker(shellparse.Command{Name: "docker", Args: args}, parseFlags(args)); len(fs) > 0 && fs[0].Level == R0 {
			t.Errorf("%v must not be read-only: %+v", args, fs)
		}
	}
}

func TestMgmtSubSkipsComposeOptionValues(t *testing.T) {
	for args, want := range map[string]string{
		"compose -p config down -v": "down", "compose --project-name=web ps": "ps", "compose -f a.yml -f b.yml logs": "logs",
		"compose --dry-run ps": "ps", "container ls": "ls", "container --foo ls": "", "compose -p": "", "ps": "",
	} {
		if got := mgmtSub(parseFlags(strings.Fields(args))); got != want {
			t.Errorf("%q: %q, want %q", args, got, want)
		}
	}
	if mgmtSub(parseFlags(nil)) != "" {
		t.Error("no words, no subcommand")
	}
}
