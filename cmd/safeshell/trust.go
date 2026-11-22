package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/policy"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// trustSet is what the person chose to always run in a folder, and whether
// that is allowed at all. It is off, with nothing trusted, when strict mode is
// on (yours or the team's), when the team policy turns it off, and when the
// settings file or the team policy cannot be used: trust only applies when
// everything that could forbid it was read.
func trustSet() (set []policy.Trust, on bool) {
	c, err := config.Load(config.Path())
	if err != nil {
		return nil, false
	}
	t, _, err := teamFile()
	if err != nil || t.NoTrust || t.Strict || c.Strict {
		return nil, false
	}
	return c.Trusted, true
}

// saveTrust adds t to the settings; a failure is said, and the command still runs this once.
func saveTrust(t policy.Trust, stderr io.Writer) {
	if !strings.HasPrefix(t.Dir, "/") { // the folder is unknown (or relative), so "this folder" means nothing
		fmt.Fprintln(stderr, "SafeShell could not tell which folder this is, so it will ask again next time.")
		return
	}
	c, err := config.Load(config.Path())
	if err == nil {
		for _, old := range c.Trusted {
			if old == t {
				return
			}
		}
		err = config.SetTrusted(config.Path(), append(c.Trusted, t))
	}
	if err != nil {
		fmt.Fprintln(stderr, "SafeShell could not save that choice, so it will ask again next time:", err)
		return
	}
	fmt.Fprintln(stderr, "Saved. SafeShell will not ask again for this exact command in this folder. See or undo that with: safeshell trust list")
}

// runTrust is `safeshell trust list|remove N|clear`.
func runTrust(args []string, stdout, stderr io.Writer) int {
	valid := len(args) == 1 && (args[0] == "list" || args[0] == "clear") || len(args) == 2 && args[0] == "remove"
	if !valid {
		fmt.Fprint(stderr, "safeshell trust: use  safeshell trust list,  safeshell trust remove N  or  safeshell trust clear\n")
		return 2
	}
	c, err := config.Load(config.Path())
	if err != nil {
		fmt.Fprintln(stderr, "safeshell trust:", err)
		return 1
	}
	list := c.Trusted
	switch args[0] {
	case "list":
		if len(list) == 0 {
			fmt.Fprintln(stdout, "Nothing is trusted. When SafeShell asks \"Run it anyway?\" for a low-risk command, answer a to stop being asked about that exact command in that folder.")
			return 0
		}
		for i, t := range list {
			fmt.Fprintf(stdout, "%d. %s   (in %s)\n", i+1, report.Printable(t.Line), report.Printable(t.Dir))
		}
		fmt.Fprintln(stdout, "Stop trusting one with: safeshell trust remove N")
		return 0
	case "remove":
		n, err := strconv.Atoi(args[1])
		if err != nil || n < 1 || n > len(list) {
			fmt.Fprintf(stderr, "safeshell trust: %q is not on the list. Run  safeshell trust list  to see the numbers.\n", args[1])
			return 1
		}
		list = append(list[:n-1:n-1], list[n:]...)
	case "clear":
		list = nil
	}
	if err := config.SetTrusted(config.Path(), list); err != nil {
		fmt.Fprintln(stderr, "safeshell trust: cannot save the setting:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Done. SafeShell will ask again.")
	return 0
}
