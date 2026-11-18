package rules

import (
	"path"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// WritePaths lists the absolute paths the commands would change, read from
// their arguments and redirects alone (relative ones are taken from cwd). It is
// what a snapshot can save when no sandbox run says which files change.
// complete is false when some operand could not be named, such as a glob, a
// variable or a command that takes its files from elsewhere.
func WritePaths(cmds []shellparse.Command, cwd string) (paths []string, complete bool) {
	complete = true
	add := func(c shellparse.Command, p string) {
		switch {
		case c.Cwd == "?" || p == "" || strings.ContainsAny(p, "*?[]{}$`~"):
			complete = false
		case strings.HasPrefix(p, "/dev/"):
		default:
			base := cwd
			if c.Cwd != "" {
				base = under(cwd, c.Cwd)
			}
			paths = append(paths, under(base, p))
		}
	}
	for _, c := range cmds {
		ops, ok := operandPaths(c)
		complete = complete && ok
		for _, p := range ops {
			add(c, p)
		}
		for _, r := range c.Redirs {
			if slices.Contains([]string{">", ">>", ">|", "&>", "&>>", "<>"}, r.Op) {
				add(c, r.Target)
			}
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths), complete
}

// under makes p absolute, taking relative paths from dir.
func under(dir, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(dir, p)
}

// writers are the commands whose files are read from elsewhere when run through xargs or find.
var writers = map[string]bool{"rm": true, "mv": true, "cp": true, "shred": true, "srm": true, "truncate": true, "chmod": true, "chown": true, "chgrp": true, "unlink": true, "tee": true, "sed": true, "perl": true}

// operandPaths are the files c's own arguments name as the ones it changes.
// ok is false when they cannot all be known. A command not listed names none
// and is still ok: only its redirects can be told.
func operandPaths(c shellparse.Command) (ops []string, ok bool) {
	if c.ViaXargs || c.ViaFind {
		return nil, !writers[c.Name]
	}
	args := c.Args
	switch c.Name {
	case "sed":
		if !slices.ContainsFunc(args, isInPlace) {
			return nil, true
		}
		pos := parseFlags(dropValues(args, "-e", "-f", "--expression", "--file")).pos
		script := slices.ContainsFunc(args, func(a string) bool {
			return a == "-e" || a == "-f" || strings.HasPrefix(a, "--expression") || strings.HasPrefix(a, "--file")
		})
		if !script && len(pos) > 0 {
			pos = pos[1:] // the first word is the script
		}
		return pos, true
	case "perl":
		if !slices.ContainsFunc(args, isInPlace) {
			return nil, true
		}
		return parseFlags(dropValues(args, "-e", "-E")).pos, true
	case "truncate":
		return parseFlags(dropValues(args, "-s", "--size", "-r", "--reference")).pos, true
	case "rm", "unlink", "shred", "srm", "tee":
		return parseFlags(args).pos, true
	case "mv", "cp":
		if slices.ContainsFunc(args, func(a string) bool {
			return a == "-t" || a == "--target-directory" || strings.HasPrefix(a, "--target-directory=")
		}) {
			return nil, false
		}
		pos := parseFlags(args).pos
		if c.Name == "cp" && len(pos) > 0 {
			pos = pos[len(pos)-1:] // only the destination changes
		}
		return pos, true
	case "chmod", "chown", "chgrp":
		if pos := parseFlags(args).pos; len(pos) > 0 {
			return pos[1:], true // the first is the mode or owner
		}
	}
	return nil, true
}

func isInPlace(a string) bool {
	return strings.HasPrefix(a, "--in-place") || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "i"))
}

// dropValues removes each listed option together with the word after it.
func dropValues(args []string, opts ...string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		if slices.Contains(opts, args[i]) {
			i++
			continue
		}
		out = append(out, args[i])
	}
	return out
}
