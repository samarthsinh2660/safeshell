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
	for sh, bind := range map[string]string{"bash": `bind -m $__ss_k -x '"\C-x\C-v": __ss_voice'`, "zsh": `bindkey '^X^V' __ss_voice`} {
		s, _ := Script(sh)
		for _, want := range []string{bind, "safeshell voice --text </dev/tty 2>/dev/tty", `[[ -n "${SAFESHELL_OFF-}" ]] && return 0`} {
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

func TestBashBindsEveryKeymapAndDisablesEditingKeys(t *testing.T) {
	s, _ := Script("bash")
	for _, want := range []string{
		"for __ss_k in emacs vi-insert vi-command",
		`bind -m $__ss_k '"\C-m": "\C-x\C-s\e[9001~"'`,
		`bind -m $__ss_k '"\C-j": "\C-x\C-s\e[9001~"'`,
		`bind -m $__ss_k '"\C-o": "\C-x\C-s\e[9002~"'`,
		`bind -m $__ss_k -x '"\C-x\C-e": __ss_noedit'`,
		`bind -m $__ss_k -x '"\e#": __ss_noedit'`,
		`bind -m vi-command -x '"v": __ss_noedit'`,
		`bind -m vi-command -x '"#": __ss_noedit'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("bash hook is missing %q", want)
		}
	}
	if strings.Contains(s, `"\C-x\C-a"`) || strings.Contains(s, `"\C-x\C-o"`) {
		t.Error("the helper bindings must not be keys a person can press by accident")
	}
}

func TestHooksSurviveNounsetAndFinishWithTheBuiltinWidget(t *testing.T) {
	b, _ := Script("bash")
	z, _ := Script("zsh")
	for name, script := range map[string]string{"bash": b, "zsh": z} {
		if strings.Contains(script, `"$SAFESHELL_OFF"`) {
			t.Errorf("%s hook reads SAFESHELL_OFF without a default, which breaks under nounset", name)
		}
	}
	for _, want := range []string{"emulate -L zsh", "zle .$1", `__ss_submit $__ss_w`, "zle -I; print -u2"} {
		if !strings.Contains(z, want) {
			t.Errorf("zsh hook is missing %q", want)
		}
	}
	if strings.Contains(z, "WIDGET") && !strings.Contains(z, "whatever name") {
		t.Error("zsh must not finish with $WIDGET, which is the user's widget name when theirs calls accept-line")
	}
	if strings.Count(z, "emulate -L zsh") < 3 {
		t.Error("every zsh function must run under emulate -L zsh")
	}
}
