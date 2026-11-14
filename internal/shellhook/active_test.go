package shellhook

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// The hook marks interactive shells so safeshell doctor can tell it is active; scripts get nothing.
func TestHookMarksOnlyInteractiveShells(t *testing.T) {
	for sh, flag := range map[string]string{"bash": "--norc", "zsh": "-f"} {
		bin, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		script, _ := Script(sh)
		for mode, same := range map[string]bool{"-ic": true, "-c": false} {
			cmd := exec.Command(bin, flag, mode, `eval "$1"; echo "$SAFESHELL_HOOK $$"`, "_", script)
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
