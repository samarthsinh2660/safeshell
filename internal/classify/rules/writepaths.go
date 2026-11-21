package rules

import (
	"path"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Written is a file a command changes, named from its arguments.
type Written struct {
	Path string `json:"path"`
	// Into is set for the destination of mv or cp: the file that changes when
	// Path turns out to be an existing folder (the source keeps its name inside it).
	Into string `json:"into,omitempty"`
}

// WritePaths lists the files the commands would change, read from their
// arguments and redirects alone (relative paths are taken from cwd). It is
// what a snapshot can save when no sandbox run says which files change.
// complete is false when some operand could not be named, such as a glob, a
// variable or a command that takes its files from elsewhere.
func WritePaths(cmds []shellparse.Command, cwd string) (out []Written, complete bool) {
	complete = true
	abs := func(c shellparse.Command, p string) (string, bool) {
		switch {
		case c.Cwd == "?" || p == "" || strings.ContainsAny(p, "*?[]{}$`~"):
			complete = false
			return "", false
		case strings.HasPrefix(p, "/dev/"):
			return "", false
		}
		base := cwd
		if c.Cwd != "" {
			base = under(cwd, c.Cwd)
		}
		return under(base, p), true
	}
	add := func(c shellparse.Command, p string) {
		if a, ok := abs(c, p); ok {
			out = append(out, Written{Path: a})
		}
	}
	for _, c := range cmds {
		ops, ok := operandPaths(c)
		complete = complete && ok
		if (c.Name == "mv" || c.Name == "cp" || c.Name == "ln" || c.Name == "rsync") && len(ops) >= 2 {
			srcs, dst := ops[:len(ops)-1], ops[len(ops)-1]
			if c.Name == "mv" {
				for _, s := range srcs {
					add(c, s)
				}
			}
			intoDir := strings.HasSuffix(dst, "/") || len(srcs) > 1
			for _, s := range srcs {
				d, ok := abs(c, dst)
				if !ok {
					break
				}
				into := path.Join(d, path.Base(s))
				switch {
				case intoDir:
					out = append(out, Written{Path: into})
				default:
					out = append(out, Written{Path: d, Into: into})
				}
			}
			ops = nil
		}
		for _, p := range ops {
			add(c, p)
		}
		for _, r := range c.Redirs {
			if slices.Contains([]string{">", ">>", ">|", "&>", "&>>", "<>"}, r.Op) {
				add(c, r.Target)
			}
		}
	}
	slices.SortFunc(out, func(a, b Written) int { return strings.Compare(a.Path+"\x00"+a.Into, b.Path+"\x00"+b.Into) })
	return slices.Compact(out), complete
}

// under makes p absolute, taking relative paths from dir.
func under(dir, p string) string {
	if path.IsAbs(p) {
		return path.Clean(p)
	}
	return path.Join(dir, p)
}

// writers are the commands whose files are read from elsewhere when run through xargs or find.
var writers = map[string]bool{"rm": true, "mv": true, "cp": true, "shred": true, "srm": true, "truncate": true, "chmod": true, "chown": true, "chgrp": true, "unlink": true, "tee": true, "sed": true, "perl": true, "touch": true, "mkdir": true, "ln": true, "rsync": true}

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
	case "touch":
		return parseFlags(dropValues(args, "-d", "-r", "-t", "--date", "--reference")).pos, true
	case "mkdir":
		return parseFlags(dropValues(args, "-m", "--mode")).pos, true
	case "ln":
		if slices.ContainsFunc(args, isTargetDir) {
			return nil, false
		}
		pos := parseFlags(args).pos
		if len(pos) == 1 { // ln -s /a/b makes ./b
			pos = []string{path.Base(pos[0])}
		}
		return pos, true // WritePaths splits the target from the link
	case "rsync":
		pos := parseFlags(dropValues(args, "-e", "--rsh", "-f", "--filter", "--exclude", "--include", "--exclude-from", "--include-from", "--files-from")).pos
		if len(pos) > 0 && remote(pos[len(pos)-1]) {
			return nil, true // it changes another machine, not a file here
		}
		return pos, true // WritePaths splits sources from the destination
	case "mv", "cp":
		if slices.ContainsFunc(args, isTargetDir) {
			return nil, false
		}
		return parseFlags(args).pos, true // WritePaths splits sources from the destination
	case "chmod", "chown", "chgrp":
		if pos := parseFlags(args).pos; len(pos) > 0 {
			return pos[1:], true // the first is the mode or owner
		}
	}
	return nil, true
}

// isTargetDir is mv, cp and ln's -t option: the destination comes first, which is not read here.
func isTargetDir(a string) bool {
	return a == "-t" || a == "--target-directory" || strings.HasPrefix(a, "--target-directory=")
}

// remote is an rsync or scp operand on another machine: host:path, with no slash before the colon.
func remote(p string) bool {
	i := strings.IndexByte(p, ':')
	return i > 0 && !strings.Contains(p[:i], "/")
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
