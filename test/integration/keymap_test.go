package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/shellhook"
)

// guardedShell starts a shell with the real hook script but a fake `safeshell`
// that stops any line containing "touch" (exit 1, prints GUARD-STOPPED) and
// passes everything else through. So these tests need neither the sandbox nor
// the built binary. It returns the shell, a file the dangerous line would
// create, and the path of the hook script to source.
func guardedShell(t *testing.T, shell string, argv []string) (*term, string, string) {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skip(shell + " is not installed")
	}
	bin := t.TempDir()
	fake := "#!/bin/sh\ncase \"$1\" in guard) ;; *) exit 0 ;; esac\nfor a; do l=$a; done\ncase \"$l\" in *touch*) echo GUARD-STOPPED >&2; exit 1 ;; esac\nprintf %s \"$l\"\n"
	if err := os.WriteFile(filepath.Join(bin, "safeshell"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(bin, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\ntouch \""+filepath.Join(bin, "edited")+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	script, err := shellhook.Script(shell)
	if err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(bin, "hook."+shell)
	if err := os.WriteFile(hook, []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	sh := startShell(t, argv, "PATH="+bin+":"+os.Getenv("PATH"), "EDITOR="+editor, "VISUAL="+editor)
	sh.waitFor(t, "READY>")
	return sh, filepath.Join(bin, "danger"), hook
}

// stopped fails the test if the dangerous line ran, once the shell has processed everything sent so far.
func stopped(t *testing.T, sh *term, marker, why string) {
	t.Helper()
	sh.send("echo alive-" + why + "\r")
	sh.waitFor(t, "alive-"+why+"\r\n")
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("%s ran the dangerous line without the guard", why)
	}
}

// press sends each key with a short pause, so Esc is not read as the start of an escape sequence.
func press(sh *term, keys ...string) {
	for _, k := range keys {
		sh.send(k)
		time.Sleep(200 * time.Millisecond)
	}
}

var (
	bashArgv = []string{"bash", "--norc", "--noprofile", "-i"}
	zshArgv  = []string{"zsh", "-f", "-i"}
)

func TestBashViModeCannotBypassTheGuard(t *testing.T) {
	for _, c := range []struct {
		name    string
		viFirst bool // vi mode on before the hook is loaded
		keys    []string
	}{
		{"vi before the hook, insert Enter", true, []string{"\r"}},
		{"vi after the hook, insert Enter", false, []string{"\r"}},
		{"vi before the hook, command Enter", true, []string{"\x1b", "\r"}},
		{"vi after the hook, command Enter", false, []string{"\x1b", "\r"}},
		{"vi insert Ctrl-J", false, []string{"\n"}},
		{"vi command Ctrl-J", false, []string{"\x1b", "\n"}},
		{"vi insert Ctrl-O", false, []string{"\x0f"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, marker, hook := guardedShell(t, "bash", bashArgv)
			if c.viFirst {
				press(sh, "set -o vi\r")
			}
			press(sh, ". "+hook+"\r")
			if !c.viFirst {
				press(sh, "set -o vi\r")
			}
			press(sh, "touch "+marker)
			press(sh, c.keys...)
			sh.waitFor(t, "GUARD-STOPPED")
			stopped(t, sh, marker, "vi")
		})
	}
}

func TestEditAndExecuteCannotRunALineUnchecked(t *testing.T) {
	for _, c := range []struct {
		name string
		vi   bool
		keys []string
	}{
		{"emacs Ctrl-X Ctrl-E", false, []string{"\x18\x05"}},
		{"vi insert Ctrl-X Ctrl-E", true, []string{"\x18\x05"}},
		{"vi command v", true, []string{"\x1b", "v"}},
		{"emacs insert-comment with a number", false, []string{"\x1b1", "\x1b#"}},
		{"vi command insert-comment with a number", true, []string{"\x1b", "1", "#"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, marker, hook := guardedShell(t, "bash", bashArgv)
			press(sh, ". "+hook+"\r")
			if c.vi {
				press(sh, "set -o vi\r")
			}
			press(sh, "touch "+marker)
			press(sh, c.keys...)
			sh.waitFor(t, "turned off by SafeShell")
			press(sh, "\x03") // drop the unsent line
			stopped(t, sh, marker, "editor")
			if _, err := os.Stat(filepath.Join(filepath.Dir(marker), "edited")); err == nil {
				t.Fatal("the editor was started, so the line would have run unchecked")
			}
		})
	}
}

func TestHookStillGuardsUnderNounset(t *testing.T) {
	for _, c := range []struct {
		name  string
		shell string
		argv  []string
		set   string
		first bool // nounset is on before the hook is loaded
	}{
		{"bash, nounset before the hook", "bash", bashArgv, "set -u", true},
		{"bash, nounset after the hook", "bash", bashArgv, "set -u", false},
		{"zsh, nounset before the hook", "zsh", zshArgv, "setopt nounset", true},
		{"zsh, nounset after the hook", "zsh", zshArgv, "setopt nounset", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, marker, hook := guardedShell(t, c.shell, c.argv)
			if c.first {
				press(sh, c.set+"\r")
			}
			press(sh, ". "+hook+"\r")
			if !c.first {
				press(sh, c.set+"\r")
			}
			press(sh, "touch "+marker+"\r")
			sh.waitFor(t, "GUARD-STOPPED")
			stopped(t, sh, marker, "nounset")
		})
	}
}

func TestZshViCommandModeCannotBypassTheGuard(t *testing.T) {
	sh, marker, hook := guardedShell(t, "zsh", zshArgv)
	press(sh, ". "+hook+"\r", "bindkey -v\r", "touch "+marker, "\x1b", "\r")
	sh.waitFor(t, "GUARD-STOPPED")
	stopped(t, sh, marker, "zsh-vicmd")
}

func TestTheHelperKeysCannotRunALineUnchecked(t *testing.T) {
	sh, marker, hook := guardedShell(t, "bash", bashArgv)
	press(sh, ". "+hook+"\r", "touch "+marker, "\x18\x01") // Ctrl-X Ctrl-A used to accept the line as is
	press(sh, "\x03")
	stopped(t, sh, marker, "ctrlx")
}

func TestZshCustomEnterWidgetStillRunsLinesAndIsGuarded(t *testing.T) {
	sh, marker, hook := guardedShell(t, "zsh", zshArgv)
	press(sh, ". "+hook+"\r", "my-enter() { zle accept-line }; zle -N my-enter; bindkey '^M' my-enter\r")
	press(sh, "echo safe-line-ran\r")
	sh.waitFor(t, "safe-line-ran\r\n")
	press(sh, "touch "+marker+"\r")
	sh.waitFor(t, "GUARD-STOPPED")
	stopped(t, sh, marker, "zsh-custom")
}
