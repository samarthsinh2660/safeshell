package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In strict mode only a line that really ran unchecked is logged as not-checked,
// a cancelled line is not, and SAFESHELL_OFF (refused there) logs no "checks off".
func TestStrictHooksLogOnlyLinesThatRanUnchecked(t *testing.T) {
	for _, c := range allHookShells {
		t.Run(c.name, func(t *testing.T) {
			d := t.TempDir()
			seen := filepath.Join(d, "seen")
			wrap := filepath.Join(d, "safeshell")
			script := "#!/bin/sh\nif [ \"$1\" = note ]; then echo \"$@\" >> " + seen + "; exit 0; fi\nexit 7\n"
			if err := os.WriteFile(wrap, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			victim := victimDir(t)
			sh := startShell(t, c.argv, "XDG_CONFIG_HOME="+strictConfig(t), "SAFESHELL_OFF=1")
			sh.waitFor(t, "READY>")
			printed := ssBin + " hook " + c.name + " | sed 's#" + ssBin + "#" + wrap + "#g'" // the hook now calls the fake, which always fails
			if c.name == "fish" {
				sh.send(printed + " | source\r")
			} else {
				sh.send(`eval "$(` + printed + `)"` + "\r")
			}
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "strict mode is on")
			sh.send("ls " + victim + "\r")
			sh.waitFor(t, "a.txt\r\n")
			if n := settle(seen, "note not-checked 7\n", 1); n != 1 {
				t.Fatalf("%d notes after one read-only line and one cancelled line, want 1", n)
			}
			if b, _ := os.ReadFile(seen); strings.Contains(string(b), "checks-off") {
				t.Errorf("strict mode logged checks-off:\n%s", b)
			}
		})
	}
}
