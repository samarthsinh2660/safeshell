package report

import (
	"slices"
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
	Programs    []string          // the programs the line runs (see Programs)
}

// Programs are the programs a classified line runs: the first word of each
// finding's command that is not a variable assignment, and the commands the
// rules do not know.
func Programs(v domain.Verdict) []string {
	var out []string
	for _, f := range v.Findings {
		for _, w := range strings.Fields(f.Command) {
			if !strings.Contains(w, "=") {
				out = append(out, w)
				break
			}
		}
	}
	return append(out, v.Unclassified...)
}

// SaferAlternative says whether alt, a replacement proposed for orig, may be
// offered. It must be rated strictly lower, not be refused outright, rest on
// observed or previewed evidence only, use only programs orig uses, and change
// nothing orig does not: orig changes files, every path alt changes is one orig
// changes, and it deletes only what orig deletes.
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
	// The check below compares files, so it only proves something when the original
	// changes files, and the proposal must use the same programs: a different tool
	// (kubectl, ssh, systemctl) can do harm no file diff shows.
	if !proven || orig.Diff == nil || alt.Diff == nil || len(orig.Diff.Changes) == 0 {
		return false
	}
	for _, p := range alt.Programs {
		if !slices.Contains(orig.Programs, p) {
			return false
		}
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
