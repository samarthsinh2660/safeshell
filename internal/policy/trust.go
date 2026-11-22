package policy

import (
	"path"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// Trust is one "always run this here" answer: an exact command line and the
// absolute folder it was typed in. It is saved in the user's own settings and
// never read from a team policy or a project file, which cannot add trust.
type Trust struct {
	Line string `json:"line"`
	Dir  string `json:"dir"`
}

// NewTrust is the Trust for a line typed in cwd. The line is kept exactly as
// typed apart from spaces at either end, and the folder is cleaned, so the
// match stays exact: no patterns, no other folder, no other line.
func NewTrust(line, cwd string) Trust {
	return Trust{Line: strings.TrimSpace(line), Dir: path.Clean(cwd)}
}

// TrustCase is what trust is asked about: the line, the folder, how it was
// rated and how that is known.
type TrustCase struct {
	Set       []Trust
	Line, Cwd string
	RuleLevel domain.Level    // the level from the rules alone
	Level     domain.Level    // the final level
	Effects   []domain.Effect // every finding, from the sandbox and the inspectors
	// Flagged is true when anything else had a say: a team rule, a project
	// rule, a protected folder, a prod host, an AI raise, a warning, or a
	// command the rules did not recognise.
	Flagged bool
}

// Eligible says whether trust may ever apply to this case, whatever is in the
// set: it must be exactly R2 from the rules and still R2 after everything
// else, with every finding seen for real (OBSERVED or PREVIEWED; an estimate or
// a read-only look is not enough), and nothing else flagged.
func (c TrustCase) Eligible() bool {
	if c.Flagged || c.RuleLevel != domain.R2 || c.Level != domain.R2 || len(c.Effects) == 0 {
		return false
	}
	for _, e := range c.Effects {
		if e.Evidence < domain.Previewed {
			return false
		}
	}
	return true
}

// DecideTrusted turns Confirm into Allow when the person trusted this exact
// line in this folder and the case is eligible. Every other decision, and
// every other case, is returned unchanged: trust never lets through a block.
func DecideTrusted(d domain.Decision, c TrustCase) domain.Decision {
	if d != domain.Confirm || !c.Eligible() {
		return d
	}
	line, cwd := strings.TrimSpace(c.Line), path.Clean(c.Cwd)
	for _, t := range c.Set {
		if t.Line == line && t.Dir == cwd {
			return domain.Allow
		}
	}
	return d
}
