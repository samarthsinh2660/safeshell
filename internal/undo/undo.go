// Package undo builds inverse plans for changes that are not files: the
// packages an apt install added and the state a systemctl command changed. A
// plan is only text, a command line and a plain-English explanation. Nothing
// here reads files or runs anything; the adapters in undo/host do that, and a
// plan runs only through the normal guard, as if the person had typed it.
package undo

import (
	"regexp"
	"strings"
)

// Plan is a proposed command that reverses part of what a command did.
type Plan struct {
	Command string `json:"command"` // one line, for the guard
	Explain string `json:"explain"` // plain English: what it undoes and what it does not
}

// plain says whether line is a single simple command, which is the only kind
// whose effect a plan can name exactly.
func plain(line string) bool { return !strings.ContainsAny(line, ";&|<>$`\n()") }

// words splits a simple command line and drops a leading sudo with its options.
func words(line string) []string {
	if !plain(line) {
		return nil
	}
	w := strings.Fields(line)
	if len(w) > 0 && w[0] == "sudo" {
		w = w[1:]
		for len(w) > 0 && strings.HasPrefix(w[0], "-") {
			w = w[1:]
		}
	}
	return w
}

var (
	pkgName  = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]*(:[a-z0-9\-]+)?$`)
	unitName = regexp.MustCompile(`^[A-Za-z0-9:_.@\-]+$`)
)
