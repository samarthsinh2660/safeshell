package shellhook

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestScriptsExistAndParse(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		s, err := Script(sh, "/opt/ss/safeshell")
		if err != nil || !strings.Contains(s, " guard ") || !strings.Contains(s, "--cwd") {
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
	if _, err := Script("nu", "/opt/ss/safeshell"); err == nil {
		t.Error("an unknown shell must say so")
	}
}

func TestScriptsBindVoice(t *testing.T) {
	for sh, bind := range map[string]string{"bash": `bind -m $__ss_k -x '"\C-x\C-v": __ss_voice'`, "zsh": `bindkey '^X^V' __ss_voice`} {
		s, _ := Script(sh, "/opt/ss/safeshell")
		for _, want := range []string{bind, `"$__ss_bin" voice --text </dev/tty 2>/dev/tty`, `[[ -n "${SAFESHELL_OFF-}" ]] && return 0`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s hook is missing %q", sh, want)
			}
		}
	}
	if s, _ := Script("bash", "/opt/ss/safeshell"); !strings.Contains(s, "READLINE_LINE=$out") {
		t.Error("bash must put the transcript on the line")
	}
	if s, _ := Script("zsh", "/opt/ss/safeshell"); !strings.Contains(s, "BUFFER=$out") {
		t.Error("zsh must put the transcript on the line")
	}
}

func TestBashBindsEveryKeymapAndDisablesEditingKeys(t *testing.T) {
	s, _ := Script("bash", "/opt/ss/safeshell")
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
	b, _ := Script("bash", "/opt/ss/safeshell")
	z, _ := Script("zsh", "/opt/ss/safeshell")
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

func TestScriptsCallTheBinaryTheyWereMadeWithAndQuoteItsPath(t *testing.T) {
	for _, sh := range []string{"bash", "zsh"} {
		s, _ := Script(sh, "/opt/o'k/safeshell")
		if !strings.Contains(s, `__ss_bin='/opt/o'\''k/safeshell'`) {
			t.Errorf("%s hook does not name its binary", sh)
		}
		if strings.Contains(s, "command safeshell") || strings.Contains(s, "@BIN@") {
			t.Errorf("%s hook still depends on PATH or kept its placeholder", sh)
		}
		if strings.Count(s, `"$__ss_bin" guard`) != 1 || strings.Count(s, `"$__ss_bin" voice`) != 1 {
			t.Errorf("%s hook must call guard and voice through __ss_bin", sh)
		}
	}
}

func TestScriptsDoNotCheckContinuationLinesOneByOne(t *testing.T) {
	b, _ := Script("bash", "/b")
	z, _ := Script("zsh", "/b")
	for _, want := range []string{"line=$__ss_pend$READLINE_LINE", "kill -INT $$", `PROMPT_COMMAND="__ss_reset`} {
		if !strings.Contains(b, want) {
			t.Errorf("bash hook is missing %q", want)
		}
	}
	for _, want := range []string{"line=$PREBUFFER$BUFFER", "zle send-break"} {
		if !strings.Contains(z, want) {
			t.Errorf("zsh hook is missing %q", want)
		}
	}
}

func TestFishQuotesTheBinaryPath(t *testing.T) {
	for bin, want := range map[string]string{
		"/opt/ss/safeshell":   `'/opt/ss/safeshell'`,
		"/home/o'brien/ss":    `'/home/o\'brien/ss'`,
		`/home/back\slash/ss`: `'/home/back\\slash/ss'`,
	} {
		s, _ := Script("fish", bin)
		if !strings.Contains(s, "function __ss_bin\n  "+want+" $argv\n") {
			t.Errorf("%s: the hook must call %s", bin, want)
		}
	}
}

func TestFishBindsEveryWayToRunALineAndVoice(t *testing.T) {
	s, _ := Script("fish", "/opt/ss/safeshell")
	for _, want := range []string{
		"for __ss_m in default insert visual replace replace_one",
		`bind -M $__ss_m \r expand-abbr __ss_guard`,
		`bind -M $__ss_m \n expand-abbr __ss_guard`,
		`bind -M $__ss_m \cx\cv __ss_voice`,
		"if status is-interactive",
		"set -gx SAFESHELL_HOOK $fish_pid",
		`test -n "$SAFESHELL_OFF"; and return 0`,
		`test -e "$__ss_off"; and return 0`,
		`test -n "$SAFESHELL_OFF"; or test -e "$__ss_off"`,
		"__ss_bin voice --text </dev/tty 2>/dev/tty",
		"__ss_bin guard $multiline --cwd \"$PWD\" $unknown -- \"$line\" </dev/tty 2>/dev/tty",
		"commandline --is-valid; or set multiline --multiline",
		"type -q -- (commandline -o)[1]; or set unknown --unknown",
		"this line was NOT checked",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("fish hook is missing %q", want)
		}
	}
}
