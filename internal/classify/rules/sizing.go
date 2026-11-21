package rules

import (
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// deletes are the commands that only remove names: the sandbox records a
// deletion as a marker, so they put no file data in its scratch layer.
var deletes = map[string]bool{"rm": true, "unlink": true, "rmdir": true, "ln": true}

// copies read their sources and put the data somewhere else.
var copies = map[string]bool{"cp": true, "mv": true, "rsync": true}

// SizingPaths lists the places whose data a simulated run could copy into the
// sandbox's scratch layer, read from the arguments alone: the files cp, mv and
// rsync read from, and the files the other writers change (a change copies the
// whole file up). rm and its kin are left out. A command whose files cannot be
// named (a glob, a variable, xargs) or that the rules do not know (unknown, such
// as "make") is sized by the folder it runs in, which is what it most likely
// works on.
func SizingPaths(cmds []shellparse.Command, cwd string, unknown []string) []string {
	var out []string
	for _, c := range cmds {
		if deletes[c.Name] {
			continue
		}
		base := cwd
		if c.Cwd != "" && c.Cwd != "?" {
			base = under(cwd, c.Cwd)
		}
		ops, ok := operandPaths(c)
		if srcs, hasT := targetDirSources(c); hasT {
			ops, ok = srcs, true
		} else if copies[c.Name] {
			ops = ops[:max(len(ops)-1, 0)] // the last one is where it goes: new data, not a source
		}
		for _, r := range c.Redirs {
			if slices.Contains([]string{">", ">>", ">|", "&>", "&>>", "<>"}, r.Op) {
				ops = append(ops, r.Target)
			}
		}
		named := false
		for _, p := range ops {
			switch {
			case strings.ContainsAny(p, "*?[]{}$`~"):
				ok = false
			case p != "" && !strings.HasPrefix(p, "/dev/"):
				out = append(out, under(base, p))
				named = true
			}
		}
		if !ok || (!named && slices.Contains(unknown, c.Name)) {
			out = append(out, base)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// targetDirSources returns, for cp and mv given -t DIR, the operands that are
// sources: with -t every operand is one and DIR is where they go. The second
// result is false when the command has no -t.
func targetDirSources(c shellparse.Command) ([]string, bool) {
	if !copies[c.Name] || c.ViaXargs || c.ViaFind || c.Name == "rsync" || !slices.ContainsFunc(c.Args, isTargetDir) {
		return nil, false
	}
	return parseFlags(dropValues(c.Args, "-t", "--target-directory")).pos, true
}
