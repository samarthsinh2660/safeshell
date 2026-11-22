package main

import (
	"fmt"
	"io"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// autorunOn says whether the assistant may run safe commands without asking;
// tests replace it so a developer's own settings never change a test.
var autorunOn = readAutorun

// readAutorun reads the setting. It is off when the settings cannot be read and
// in strict mode (yours or the team's), where nothing may run on a guess.
func readAutorun() bool {
	c, err := config.Load(config.Path())
	if err != nil || !c.Autorun || c.Strict {
		return false
	}
	t, _, err := teamFile()
	return err == nil && !t.Strict
}

// runsUnasked is true for a command the assistant wrote that may run without
// the "Run it?" question. All of these must hold: the checks allowed it at R0 or
// R1; every program in it is one of the short list of everyday file and folder
// programs (policy.AutorunSafe: no scripts, interpreters, build tools, package
// managers, git or ssh, because a low rating does not say whether a program runs
// other code); there is no policy warning and no host label on it; and it does not
// change the shell itself. Everything else keeps its question or its block.
func runsUnasked(line string, a engine.Analysis) bool {
	return autorunOn() && a.Decision == domain.Allow && a.Level <= domain.R1 && policy.AutorunSafe(a.Verdict) &&
		a.PolicyWarning == "" && a.Host.Note == "" && !changesShell(line)
}

// runAutorun is `safeshell autorun [on|off|status]`.
func runAutorun(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "on" && args[0] != "off" && args[0] != "status") {
		fmt.Fprint(stderr, usage)
		return 2
	}
	c, err := config.Load(config.Path())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell autorun:", err)
		return 1
	}
	if len(args) == 1 && args[0] != "status" {
		c.Autorun = args[0] == "on"
		if err := config.SetAutorun(config.Path(), c.Autorun); err != nil {
			fmt.Fprintln(stderr, "safeshell autorun: cannot save the setting:", err)
			return 1
		}
	}
	if c.Autorun {
		fmt.Fprintln(stdout, "Autorun is on. When you ask in plain English, safe commands run at once. Only everyday file and folder commands run this way; anything else still asks or is blocked. Turn it off: safeshell autorun off")
	} else {
		fmt.Fprintln(stdout, "Autorun is off. Every command the assistant writes asks first. Turn it on: safeshell autorun on")
	}
	return 0
}
