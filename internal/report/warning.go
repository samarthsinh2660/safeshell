package report

import (
	"path/filepath"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Touched lists the places line concerns, as absolute paths: the working
// directory, every observed change, and each argument or redirect target that
// looks like a path (starts with /, ~, ./ or ../, or contains a /; the value
// of key=value and --opt=value words; the folder before a glob character).
// Relative paths are resolved against the directory the command runs in and a
// leading ~ against home.
//
// uncertain is true when the paths cannot be known from the text alone (the
// line does not parse, uses variables, ~user, xargs, find -exec, $(...) or ssh,
// moves with a relative cd, or a command names no path at all). Callers should
// then show every note.
func Touched(line, cwd, home string, changes []sandbox.Change) (paths []string, uncertain bool) {
	t := &touch{home: home}
	if cwd != "" {
		t.paths = append(t.paths, filepath.Clean(cwd))
	}
	for _, c := range changes {
		t.paths = append(t.paths, filepath.Clean(c.Abs()))
	}
	res, err := shellparse.Parse(line)
	if err != nil {
		return t.paths, true
	}
	for _, p := range res.Pipelines {
		for _, c := range p.Cmds {
			t.command(c, cwd)
		}
	}
	return t.paths, t.unsure
}

type touch struct {
	home   string
	paths  []string
	unsure bool
}

func (t *touch) command(c shellparse.Command, cwd string) {
	base := cwd
	switch {
	case c.Cwd == "":
	case strings.HasPrefix(c.Cwd, "/"):
		base = c.Cwd
		t.paths = append(t.paths, c.Cwd)
	default:
		t.unsure = true // after a cd to ~ or somewhere unknown
	}
	// A command that follows a cd, or writes through a redirect, has a place.
	located := c.Cwd != "" || len(c.Redirs) > 0
	if c.ViaXargs || c.ViaFind || len(c.FetchedArgs) > 0 || c.Remote != "" {
		t.unsure = true
	}
	moves := c.Name == "cd" || c.Name == "pushd"
	dirs := 0
	for _, a := range c.Args {
		if t.word(base, a) {
			located = true
		}
		if moves && !strings.HasPrefix(a, "-") {
			dirs++
			if !strings.HasPrefix(a, "/") {
				t.unsure = true
			}
		}
	}
	if moves && dirs == 0 {
		t.unsure = true
	}
	for _, r := range c.Redirs {
		t.word(base, r.Target)
	}
	if !located {
		t.unsure = true
	}
}

// word records the path in a, if any, and reports whether it found one.
func (t *touch) word(base, a string) bool {
	if strings.ContainsAny(a, "$`") || (strings.HasPrefix(a, "~") && a != "~" && !strings.HasPrefix(a, "~/")) {
		t.unsure = true
		return false
	}
	if i := strings.IndexByte(a, '='); i > 0 && !strings.Contains(a[:i], "/") {
		return t.word(base, a[i+1:])
	}
	if i := strings.IndexAny(a, "*?["); i >= 0 {
		a = a[:i]
		a = a[:strings.LastIndex(a, "/")+1]
	}
	switch {
	case a == "." || a == "..":
		if base == "" {
			t.unsure = true
			return false
		}
		t.paths = append(t.paths, filepath.Join(base, a))
	case a == "~" || strings.HasPrefix(a, "~/"):
		if t.home == "" {
			t.unsure = true
			return false
		}
		t.paths = append(t.paths, filepath.Join(t.home, a[1:]))
	case strings.HasPrefix(a, "/"):
		t.paths = append(t.paths, filepath.Clean(a))
	case strings.Contains(a, "/"):
		if base == "" {
			t.unsure = true
			return false
		}
		t.paths = append(t.paths, filepath.Join(base, a))
	default:
		return false
	}
	return true
}

// RelevantWarnings keeps the warnings with a path that is an ancestor or a
// descendant of (or equal to) one of the touched paths, and every warning that
// names no path.
func RelevantWarnings(ws []sandbox.Warning, touched []string) []sandbox.Warning {
	var out []sandbox.Warning
	for _, w := range ws {
		if concerns(w, touched) {
			out = append(out, w)
		}
	}
	return out
}

func concerns(w sandbox.Warning, touched []string) bool {
	if len(w.Paths) == 0 {
		return true
	}
	for _, p := range w.Paths {
		for _, t := range touched {
			if within(p, t) || within(t, p) {
				return true
			}
		}
	}
	return false
}

// within reports whether p is dir or lies below it.
func within(p, dir string) bool {
	return p == dir || dir == "/" || strings.HasPrefix(p, dir+"/")
}
