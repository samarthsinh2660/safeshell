// Package report turns observed effects into a risk level and readable
// content. Everything here is pure: the same diff always gives the same score.
//
// One exception to the layering: report reads the data types of package sandbox
// (FileDiff, Change, NetAttempt, Warning). They are plain data with no
// behaviour that touches the system, so report stays free of file, process and
// network access.
package report

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// Impact is the deterministic score of an observed file diff.
type Impact struct {
	Level   domain.Level
	Reasons []string
}

var systemPrefixes = []string{"etc", "usr", "bin", "sbin", "lib", "lib32", "lib64", "boot", "root", "opt", "var/lib", "sys", "proc"}

func isSystemPath(p string) bool {
	p = strings.TrimPrefix(filepath.ToSlash(p), "/")
	for _, s := range systemPrefixes {
		if p == s || strings.HasPrefix(p, s+"/") {
			return true
		}
	}
	return false
}

// Score rates what a command did to the filesystem. It only looks at facts the
// sandbox observed: how much was deleted, whether system files changed, and
// whether anything deleted looks important.
func Score(d sandbox.FileDiff) Impact {
	im := Impact{Level: domain.R0}
	raise := func(l domain.Level, why string) {
		im.Level = max(im.Level, l)
		im.Reasons = append(im.Reasons, why)
	}
	t := d.Totals()
	if t.Created > 0 {
		raise(domain.R1, fmt.Sprintf("creates %d file(s)", t.Created))
	}
	if t.Modified > 0 {
		raise(domain.R2, fmt.Sprintf("changes %d existing file(s)", t.Modified))
	}
	switch {
	case t.Deleted >= 100:
		raise(domain.R3, fmt.Sprintf("deletes %d files", t.Deleted))
	case t.Deleted > 0:
		raise(domain.R2, fmt.Sprintf("deletes %d file(s)", t.Deleted))
	}
	var system, notable []string
	for _, c := range d.Changes {
		if isSystemPath(c.Path) && c.Kind != sandbox.Created {
			system = append(system, c.Abs())
		}
		if c.Kind == sandbox.Deleted || c.Kind == sandbox.Replaced {
			notable = append(notable, c.Notable...)
			if len(c.Notable) == 0 && !c.IsDir && sandbox.IsNotable(c.Path) {
				notable = append(notable, c.Path)
			}
		}
	}
	if len(system) > 0 {
		raise(domain.R3, "changes system files: "+sample(system, 3))
	}
	if len(notable) > 0 {
		raise(domain.R3, "deletes files that look important: "+sample(notable, 3))
	}
	return im
}

func sample(xs []string, n int) string {
	if len(xs) <= n {
		return strings.Join(xs, ", ")
	}
	return strings.Join(xs[:n], ", ") + fmt.Sprintf(" and %d more", len(xs)-n)
}
