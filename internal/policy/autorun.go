package policy

import (
	"path"
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// The programs the assistant may run without a question. The lists are short on
// purpose: a rating of R0 or R1 says what the rules think a command does to
// files, not whether it runs other code or overwrites something that matters, so
// interpreters, scripts, build tools, package managers, git, ssh and anything
// that can write a file through an option (sort -o, tree -o, file -C) are not on
// them, and neither is a leading VAR=value.
var (
	autorunReaders = []string{"cd", "ls", "pwd", "cat", "head", "tail", "wc", "du", "df", "grep"}
	// autorunWriters change files, so their paths and options are checked too.
	autorunWriters = []string{"mv", "cp", "mkdir", "touch"}
	// autorunWriterOptions are the only options a writer may have: no -f, -t, -T or -S.
	autorunWriterOptions = []string{"-r", "-R", "-p", "-n", "-v", "-i"}
)

// AutorunSafe reports whether every command in v may run without a question:
// each is an autorunReader or an autorunWriter, and every path a writer touches
// is inside the home folder and not hidden (so never .bashrc, .ssh or .config).
// cwd is where the line runs and home is the user's home folder. A verdict with
// no findings, with a program the rules do not know, or with a cd and a writer
// together (the cd moves what a relative path means) is not safe.
func AutorunSafe(v domain.Verdict, line, cwd, home string) bool {
	if len(v.Findings) == 0 || len(v.Unclassified) > 0 || !plainLine(line) {
		return false
	}
	var cd, write bool
	for _, f := range v.Findings {
		fields := strings.Fields(f.Command)
		if len(fields) == 0 {
			return false
		}
		switch w := fields[0]; {
		case w == "cd":
			cd = true
		case slices.Contains(autorunReaders, w):
		case slices.Contains(autorunWriters, w):
			write = true
			if !writerPathsOK(fields[1:], cwd, home) {
				return false
			}
		default:
			return false
		}
	}
	return !(cd && write)
}

// plainLine is true when the raw line is made only of plain words and the
// separators | & ;, and each part starts with a program from the lists. The
// classifier drops variable assignments, env, redirects and here-strings before it
// reports a command, so the line itself is checked for them: no $, quote,
// backtick, redirect, bracket or = at all, so nothing can hide a second command.
func plainLine(line string) bool {
	if strings.ContainsFunc(line, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune(" _-./~*,:@+%|&;", r))
	}) {
		return false
	}
	for _, part := range strings.FieldsFunc(line, func(r rune) bool { return r == '|' || r == '&' || r == ';' }) {
		f := strings.Fields(part)
		if len(f) == 0 || !(f[0] == "cd" || slices.Contains(autorunReaders, f[0]) || slices.Contains(autorunWriters, f[0])) {
			return false
		}
	}
	return true
}

// writerPathsOK is true when args are plain options and paths that all end up
// inside home, with no hidden file or folder on the way.
func writerPathsOK(args []string, cwd, home string) bool {
	if !path.IsAbs(home) || !path.IsAbs(cwd) {
		return false
	}
	home = path.Clean(home)
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			if !slices.Contains(autorunWriterOptions, a) {
				return false
			}
			continue
		}
		p := a
		switch {
		case a == "~":
			p = home
		case strings.HasPrefix(a, "~/"):
			p = home + a[1:]
		case strings.HasPrefix(a, "~"):
			return false
		case !path.IsAbs(a):
			p = path.Join(cwd, a)
		}
		p = path.Clean(p)
		rest, under := strings.CutPrefix(p, home+"/")
		if !under {
			return false
		}
		for _, part := range strings.Split(rest, "/") {
			if strings.HasPrefix(part, ".") {
				return false
			}
		}
	}
	return true
}
