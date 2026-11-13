package report

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// Learn is the one-line lesson shown in learning mode for a command that runs
// without a full report: what it does, its level and the safer form when there
// is one. It uses only what the rules already found, so it needs no AI. It is
// empty for a read-only command, which has nothing to teach.
func Learn(v domain.Verdict) string {
	if v.Level == domain.R0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Learn: " + v.Level.String() + " " + v.Level.Label() + ".")
	for _, f := range v.Findings {
		if f.Level != v.Level {
			continue
		}
		b.WriteString(" " + Printable(f.Reason))
		if f.Alt != nil {
			b.WriteString(" Safer: " + Printable(f.Alt.Command) + " (" + Printable(f.Alt.Note) + ")")
		}
		break
	}
	return b.String()
}
