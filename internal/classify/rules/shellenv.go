package rules

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// ruleExport rates export, declare and bare assignments by the variable they
// set. Most are harmless; a few change what later commands do.
func ruleExport(c shellparse.Command, _ flags) []Finding {
	var fs []Finding
	for _, a := range c.Args {
		name, val, assigned := strings.Cut(a, "=")
		appended := strings.HasSuffix(name, "+")
		name = strings.TrimSuffix(name, "+")
		switch {
		case name == "LD_PRELOAD" && val != "":
			fs = append(fs, fnd(R2, c, "Makes every program you start afterwards load code from there. A bad file there can break or take over those programs."))
		case name == "LD_LIBRARY_PATH" && assigned && !appended && writableFirst(val):
			fs = append(fs, fnd(R2, c, "Makes every program you start afterwards look for libraries in a folder anyone can write to. A planted file there would be loaded."))
		case name == "PROMPT_COMMAND":
			fs = append(fs, fnd(R2, c, "Runs a command before every prompt, so it keeps running until you close the terminal."))
		case name == "PATH" && assigned && !appended && writableFirst(val):
			fs = append(fs, fnd(R2, c, "Puts a folder anyone can write to first in your search path, so a planted program there would run instead of the real one."))
		}
	}
	if len(fs) == 0 {
		return one(fnd(R0, c, "Sets a variable in this shell."))
	}
	return fs
}

// writableFirst reports whether the first folder in a path list is one that
// other users or other programs can write to: the current folder, a relative
// folder or a shared temporary folder.
func writableFirst(val string) bool {
	first, _, _ := strings.Cut(val, ":")
	if first == "" || !strings.HasPrefix(first, "/") && !strings.HasPrefix(first, "~") && !strings.HasPrefix(first, "$") {
		return true
	}
	for _, d := range []string{"/tmp", "/var/tmp", "/dev/shm"} {
		if first == d || strings.HasPrefix(first, d+"/") {
			return true
		}
	}
	return false
}
