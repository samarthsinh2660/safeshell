package main

import (
	"fmt"
	"io"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// learning says whether learning mode is on; tests replace it so a developer's
// own settings never change a test.
var learning = readLearning

// readLearning reads the setting. A settings file that cannot be read counts
// as off: learning mode only adds output, so it must never get in the way.
func readLearning() bool {
	c, err := config.Load(config.Path())
	return err == nil && c.Learn
}

// learnNote prints the one-line lesson for a command that passes without a
// full report. It adds output only: it never changes a decision.
func learnNote(w io.Writer, a engine.Analysis, color bool) {
	// R0 teaches nothing, so skip reading the settings for the commonest case.
	if a.Verdict.Level == 0 || !learning() {
		return
	}
	c := newPalette(color)
	fmt.Fprintf(w, "%s%s%s\n", c.dim, report.Learn(a.Verdict), c.reset)
}

// runLearn is `safeshell learn [on|off|status]`.
func runLearn(args []string, stdout, stderr io.Writer) int {
	if len(args) > 1 || (len(args) == 1 && args[0] != "on" && args[0] != "off" && args[0] != "status") {
		fmt.Fprint(stderr, usage)
		return 2
	}
	c, err := config.Load(config.Path())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell learn:", err)
		return 1
	}
	if len(args) == 1 && args[0] != "status" {
		c.Learn = args[0] == "on"
		if err := config.SetLearn(config.Path(), c.Learn); err != nil {
			fmt.Fprintln(stderr, "safeshell learn: cannot save the setting:", err)
			return 1
		}
	}
	if c.Learn {
		fmt.Fprintln(stdout, "Learning mode is on. Each command that runs without a warning gets one line saying what it does and a safer form. Turn it off: safeshell learn off")
	} else {
		fmt.Fprintln(stdout, "Learning mode is off. Turn it on: safeshell learn on")
	}
	return 0
}
