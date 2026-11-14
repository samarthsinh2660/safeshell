package integration

import (
	"os"
	"testing"
)

// While the off file exists the hook must not call safeshell at all (the fake
// stops any line with "touch", so a line that ran was never checked); once it
// is removed the hook guards again. Both shells, also under nounset.
func TestPausedHookDoesNotCallSafeshell(t *testing.T) {
	for _, c := range []struct {
		name  string
		shell string
		argv  []string
		set   string
	}{
		{"bash", "bash", bashArgv, ""},
		{"bash nounset", "bash", bashArgv, "set -u"},
		{"zsh", "zsh", zshArgv, ""},
		{"zsh nounset", "zsh", zshArgv, "setopt nounset"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, marker, hook := guardedShell(t, c.shell, c.argv)
			if c.set != "" {
				press(sh, c.set+"\r")
			}
			press(sh, ". "+hook+"\r")
			off := `"${XDG_STATE_HOME}/safeshell/off"`

			press(sh, "touch "+marker+"\r") // active: stopped
			sh.waitFor(t, "GUARD-STOPPED")
			stopped(t, sh, marker, "active")

			press(sh, `mkdir -p "${XDG_STATE_HOME}/safeshell" && : > `+off+"\r") // safeshell deactivate
			press(sh, "touch "+marker+"\r")
			sh.send("echo paused-done\r")
			sh.waitFor(t, "paused-done\r\n")
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("a paused hook must let the line run without calling safeshell")
			}

			if err := os.Remove(marker); err != nil {
				t.Fatal(err)
			}
			press(sh, "rm -f "+off+"\r") // safeshell activate
			press(sh, "touch "+marker+"\r")
			sh.waitFor(t, "GUARD-STOPPED")
			stopped(t, sh, marker, "active-again")
		})
	}
}
