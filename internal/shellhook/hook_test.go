package shellhook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptsExistAndParse(t *testing.T) {
	for _, sh := range []string{"bash", "zsh"} {
		s, err := Script(sh)
		if err != nil || !strings.Contains(s, "safeshell guard") {
			t.Fatalf("%s: %v", sh, err)
		}
		bin, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		f := filepath.Join(t.TempDir(), "hook")
		_ = os.WriteFile(f, []byte(s), 0o600)
		if out, err := exec.Command(bin, "-n", f).CombinedOutput(); err != nil {
			t.Errorf("%s syntax: %v %s", sh, err, out)
		}
	}
	if _, err := Script("fish"); err == nil {
		t.Error("fish is not supported yet and must say so")
	}
}

func TestScriptsBindVoice(t *testing.T) {
	for sh, bind := range map[string]string{"bash": `bind -x '"\C-x\C-v": __ss_voice'`, "zsh": `bindkey '^X^V' __ss_voice`} {
		s, _ := Script(sh)
		for _, want := range []string{bind, "safeshell voice --text </dev/tty 2>/dev/tty", `[[ -n "$SAFESHELL_OFF" ]] && return`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s hook is missing %q", sh, want)
			}
		}
	}
	if s, _ := Script("bash"); !strings.Contains(s, "READLINE_LINE=$out") {
		t.Error("bash must put the transcript on the line")
	}
	if s, _ := Script("zsh"); !strings.Contains(s, "BUFFER=$out") {
		t.Error("zsh must put the transcript on the line")
	}
}
