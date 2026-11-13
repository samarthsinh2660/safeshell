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

func TestBashBindsEveryKeymapAndDisablesEditAndExecute(t *testing.T) {
	s, _ := Script("bash")
	for _, want := range []string{
		"for __ss_k in emacs vi-insert vi-command",
		`bind -m $__ss_k '"\C-m": "\C-x\C-s\C-x\C-a"'`,
		`bind -m $__ss_k '"\C-j": "\C-x\C-s\C-x\C-a"'`,
		`bind -m $__ss_k '"\C-o": "\C-x\C-s\C-x\C-o"'`,
		`bind -m $__ss_k -x '"\C-x\C-e": __ss_noedit'`,
		`bind -m vi-command -x '"v": __ss_noedit'`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("bash hook is missing %q", want)
		}
	}
}

func TestHooksSurviveNounsetAndWarnOnUnexpectedFailure(t *testing.T) {
	b, _ := Script("bash")
	z, _ := Script("zsh")
	for _, c := range []struct{ name, script, bad string }{
		{"bash", b, `"$SAFESHELL_OFF"`},
		{"zsh", z, `"$SAFESHELL_OFF"`},
	} {
		if strings.Contains(c.script, c.bad) {
			t.Errorf("%s hook reads SAFESHELL_OFF without a default, which breaks under nounset", c.name)
		}
	}
	for _, want := range []string{"__ss_guard_run ||", "__ss_voice_run ||", "NOT checked"} {
		if !strings.Contains(b, want) {
			t.Errorf("bash hook is missing %q", want)
		}
	}
	for _, want := range []string{"emulate -L zsh", "*) print -u2", "zle .${WIDGET}"} {
		if !strings.Contains(z, want) {
			t.Errorf("zsh hook is missing %q", want)
		}
	}
	if strings.Count(z, "emulate -L zsh") < 3 {
		t.Error("every zsh function must run under emulate -L zsh")
	}
}
