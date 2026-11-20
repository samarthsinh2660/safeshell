package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// These tests need fish (they skip when it is not installed; CI installs it).

func TestFishHookStopsADangerousCommandAndLetsSafeOnesRun(t *testing.T) {
	needSandbox(t)
	victim := victimDir(t)
	sh := hooked(t, "fish", fishArgv)
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the dangerous command ran")
	}
	sh.send("echo from-the-fish-hook\r")
	sh.waitFor(t, "from-the-fish-hook\r\n")
}

func TestFishCtrlJCannotBypassTheGuard(t *testing.T) {
	needSandbox(t)
	victim := victimDir(t)
	sh := hooked(t, "fish", fishArgv)
	sh.send("rm -rf " + victim + "\n") // a bare newline is Ctrl-J, which fish also treats as Enter
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("Ctrl-J ran the command without the guard")
	}
}

func TestFishLineRunsWithAWarningIfSafeShellIsGone(t *testing.T) {
	needSandbox(t)
	cp := filepath.Join(t.TempDir(), "safeshell")
	b, err := os.ReadFile(ssBin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cp, b, 0o755); err != nil {
		t.Fatal(err)
	}
	sh := startShell(t, fishArgv)
	sh.waitFor(t, "READY>")
	sh.send(cp + " hook fish | source\r")
	sh.send("echo hooked\r")
	sh.waitFor(t, "hooked\r\n")
	if err := os.Remove(cp); err != nil {
		t.Fatal(err)
	}
	sh.send("echo still-ran\r")
	sh.waitFor(t, "NOT checked")
	sh.waitFor(t, "still-ran\r\n")
}

// The next tests use the real hook script with a fake safeshell that stops any
// line containing "touch" (see guardedShell), so they need no sandbox.

func TestFishEveryWayOfRunningALineIsGuarded(t *testing.T) {
	for _, c := range []struct {
		name  string
		setup []string // typed before the dangerous line
		keys  []string // what ends the line
	}{
		{"Enter", nil, []string{"\r"}},
		{"Ctrl-J", nil, []string{"\n"}},
		{"vi insert Enter", []string{"fish_vi_key_bindings\r"}, []string{"\r"}},
		{"vi normal Enter", []string{"fish_vi_key_bindings\r"}, []string{"\x1b", "\r"}},
		{"vi normal Ctrl-J", []string{"fish_vi_key_bindings\r"}, []string{"\x1b", "\n"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			sh, marker, hook := guardedShell(t, "fish", fishArgv)
			press(sh, ". "+hook+"\r")
			press(sh, c.setup...)
			press(sh, "touch "+marker)
			press(sh, c.keys...)
			sh.waitFor(t, "GUARD-STOPPED")
			stopped(t, sh, marker, "fish")
		})
	}
}

func TestFishAbbreviationIsExpandedBeforeTheGuardLooksAtTheLine(t *testing.T) {
	sh, marker, hook := guardedShell(t, "fish", fishArgv)
	press(sh, ". "+hook+"\r", "abbr -a nuke \"tou\"ch "+marker+"\r") // spelled so the fake guard lets the definition through
	press(sh, "nuke\r") // expanded on the line, not run: the person sees what Enter will run
	if sh.shows("GUARD-STOPPED") {
		t.Fatal("the guard was asked about the abbreviation, not what it stands for")
	}
	press(sh, "\r")
	sh.waitFor(t, "GUARD-STOPPED")
	stopped(t, sh, marker, "abbr")
}

// An abbreviation that does not apply where it is typed is left alone: the
// second Enter checks the line as it is, rather than trying to expand forever.
func TestFishAbbreviationThatDoesNotApplyDoesNotLoop(t *testing.T) {
	sh, marker, hook := guardedShell(t, "fish", fishArgv)
	press(sh, ". "+hook+"\r", "abbr -a --position command zz 'echo nope'\r")
	press(sh, "touch "+marker+" zz\r", "\r")
	sh.waitFor(t, "GUARD-STOPPED")
	stopped(t, sh, marker, "abbr")
}

func TestFishSafeLinesRunAndAnUnfinishedOneWaitsForTheRest(t *testing.T) {
	sh, _, hook := guardedShell(t, "fish", fishArgv)
	press(sh, ". "+hook+"\r")
	press(sh, "echo safe-fish-line\r")
	sh.waitFor(t, "safe-fish-line\r\n")
	press(sh, "echo 'one")
	press(sh, "\r") // fish adds a line instead of running a half-typed quote
	sh.send("two'\r")
	sh.waitFor(t, "two\r\n")
}

func TestFishPausedHookDoesNotCallSafeshell(t *testing.T) {
	sh, marker, hook := guardedShell(t, "fish", fishArgv)
	press(sh, ". "+hook+"\r")
	press(sh, "mkdir -p $XDG_STATE_HOME/safeshell; and echo -n > $XDG_STATE_HOME/safeshell/off\r") // safeshell deactivate
	press(sh, "touch "+marker+"\r")
	sh.send("echo paused-done\r")
	sh.waitFor(t, "paused-done\r\n")
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("a paused hook must let the line run without calling safeshell")
	}
}
