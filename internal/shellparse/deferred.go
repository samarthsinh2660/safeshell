package shellparse

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// startupFiles are read by a shell every time it starts, so whatever is added
// to them runs later, unseen.
var startupFiles = map[string]bool{".bashrc": true, ".bash_profile": true, ".bash_login": true, ".bash_logout": true, ".profile": true,
	".zshrc": true, ".zshenv": true, ".zprofile": true, ".zlogin": true, "config.fish": true}

// IsStartupFile reports whether p is a shell startup file.
func IsStartupFile(p string) bool { return startupFiles[path.Base(p)] }

// deferred queues code that runs later, such as an alias value or a cron job.
// It runs somewhere else, so a `cd` in it must not leak out and the current
// directory is unknown while it is read.
func (w *walker) deferred(code string, depth int) {
	if depth >= maxDepth {
		return
	}
	saved := w.cwd
	w.cwd = "?"
	_ = w.parseString(code, depth+1)
	w.cwd = saved
}

// startupText queues the text that c writes into a shell startup file, by
// redirect or by tee, as code: it runs every time a shell starts.
func (w *walker) startupText(c *Command, depth int) {
	hit := c.Name == "tee" && anyStartupFile(c.Args)
	for _, r := range c.Redirs {
		hit = hit || (r.Op == ">>" || r.Op == ">") && IsStartupFile(r.Target)
	}
	if !hit {
		return
	}
	text := c.Stdin
	if c.Name == "echo" || c.Name == "printf" {
		text += echoText(c.Args)
	}
	w.deferred(strings.ReplaceAll(text, `\n`, "\n"), depth)
}

func anyStartupFile(args []string) bool {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") && IsStartupFile(a) {
			return true
		}
	}
	return false
}

// echoed returns the text that echo and printf write inside st, one line each.
func echoed(st *syntax.Stmt) string {
	var text string
	syntax.Walk(st, func(n syntax.Node) bool {
		if call, ok := n.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			if name := wordString(call.Args[0]); name == "echo" || name == "printf" {
				var args []string
				for _, a := range call.Args[1:] {
					args = append(args, wordString(a))
				}
				text += echoText(args)
			}
		}
		return true
	})
	return text
}

// echoText is the line echo or printf writes for args, without options such as -e.
func echoText(args []string) string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		args = args[1:]
	}
	return "\n" + strings.Join(args, " ")
}
