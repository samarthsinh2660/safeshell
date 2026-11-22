package report

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// Outcome is what a full analysis of one command line found, as far as judging
// a proposed replacement needs it.
type Outcome struct {
	Level       domain.Level
	Decision    domain.Decision
	TeamBlocked bool              // the admin's policy refuses the line
	Effects     []domain.Effect   // everything observed, inspected, previewed or estimated
	Diff        *sandbox.FileDiff // the files changed in the sandbox; nil when it was not observed
}

// SaferAlternative says whether alt, a replacement proposed for orig, may be
// offered. It must be rated strictly lower, not be refused outright, rest on
// observed or previewed evidence only, and change nothing orig does not: every
// path it changes is one orig changes, and it deletes only what orig deletes.
func SaferAlternative(orig, alt Outcome) bool {
	if alt.Level >= orig.Level || alt.Decision == domain.HardBlock || alt.TeamBlocked {
		return false
	}
	proven := false
	for _, e := range alt.Effects {
		switch e.Evidence {
		case domain.Estimated:
			return false
		case domain.Observed, domain.Previewed:
			proven = true
		}
	}
	if !proven || orig.Diff == nil || alt.Diff == nil {
		return false
	}
	for _, c := range alt.Diff.Changes {
		if !coveredBy(c, orig.Diff.Changes) {
			return false
		}
	}
	return true
}

// coveredBy is true when some change in outer is at c's path or above it, and
// c is a deletion only where that change is.
func coveredBy(c sandbox.Change, outer []sandbox.Change) bool {
	for _, o := range outer {
		if c.Path != o.Path && !(o.IsDir && strings.HasPrefix(c.Path, o.Path+"/")) {
			continue
		}
		if !removes(c) || removes(o) {
			return true
		}
	}
	return false
}

func removes(c sandbox.Change) bool { return c.Kind == sandbox.Deleted || c.Kind == sandbox.Replaced }
