package shellhook

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The hook marks interactive shells so safeshell doctor can tell it is active; scripts get nothing.
func TestHookMarksOnlyInteractiveShells(t *testing.T) {
	posix := `eval "$1"; echo "$SAFESHELL_HOOK $$"`
	for sh, c := range map[string]struct{ flag, prog string }{
		"bash": {"--norc", posix},
		"zsh":  {"-f", posix},
		"fish": {"--no-config", `printf %s $argv[2] | source; echo "$SAFESHELL_HOOK $fish_pid"`},
	} {
		bin, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		script, _ := Script(sh, "/opt/ss/safeshell")
		for mode, same := range map[string]bool{"-ic": true, "-c": false} {
			cmd := exec.Command(bin, c.flag, mode, c.prog, "_", script)
			cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
			out, _ := cmd.Output()
			f := strings.Fields(string(out))
			switch {
			case same && (len(f) != 2 || f[0] != f[1]):
				t.Errorf("%s %s: the hook must export its own shell's pid, got %q", sh, mode, out)
			case !same && len(f) != 1:
				t.Errorf("%s %s: a script must get nothing, got %q", sh, mode, out)
			}
		}
	}
}
