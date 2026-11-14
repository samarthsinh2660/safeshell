package report

import (
	"fmt"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

var decisionWhy = [...]string{
	domain.Allow:     "it runs without asking",
	domain.Confirm:   "it asks you first",
	domain.Block:     "it is blocked, and you can override",
	domain.HardBlock: "it is hard blocked, and overriding needs a typed phrase",
}

// Why explains, step by step and without any AI, how a command got its final
// level: the rules that read the text, each effect with how sure we are of it,
// and the step that set the result. v is the rule verdict, effects what the
// real system and the sandbox showed, final and d the outcome.
func Why(line string, v domain.Verdict, effects []domain.Effect, final domain.Level, d domain.Decision) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n$ %s\n", final, final.Label(), decisionWhy[d], Printable(line))

	b.WriteString("\n1. The rules (they read the command text)\n")
	setter := -1
	for i, f := range v.Findings {
		if f.Level == v.Level && setter < 0 {
			setter = i
		}
		if f.Level == domain.R0 && v.Level > domain.R0 {
			continue
		}
		fmt.Fprintf(&b, "   %s  %s\n", f.Level, Printable(f.Reason))
		if f.Command != line {
			fmt.Fprintf(&b, "       in: %s\n", Printable(f.Command))
		}
	}
	if len(v.Findings) == 0 {
		b.WriteString("   No rule matched this command.\n")
	}
	fmt.Fprintf(&b, "   Rule level: %s\n", v.Level)

	b.WriteString("\n2. What it would really do (how sure, least to most: estimated, inspected, previewed, observed)\n")
	raiser := -1
	for i, e := range effects {
		note := "does not raise the level"
		if e.MinLevel > v.Level {
			note = "raises the level to " + e.MinLevel.String()
			if e.MinLevel == final && raiser < 0 {
				raiser = i
			}
		}
		fmt.Fprintf(&b, "   %s  %s: %s (%s)\n", e.Evidence, e.Kind, Printable(e.Summary), note)
		for _, det := range e.Details {
			fmt.Fprintf(&b, "       %s\n", Printable(det))
		}
	}
	if len(effects) == 0 {
		b.WriteString("   Nothing seen here can lower the level.\n")
	}

	fmt.Fprintf(&b, "\n3. Final level: %s\n", final)
	switch {
	case raiser >= 0:
		e := effects[raiser]
		fmt.Fprintf(&b, "   Set by the %s effect (%s): it raised the level from %s to %s.\n", e.Kind, e.Evidence, v.Level, final)
	case setter >= 0:
		fmt.Fprintf(&b, "   Set by the rule: %s\n", Printable(v.Findings[setter].Reason))
	default:
		fmt.Fprintf(&b, "   No rule set it, so it stays at %s.\n", final)
	}
	if d == domain.Confirm && final < domain.R2 {
		b.WriteString("   Part of what it does could only be guessed (for example the sandbox could not run), so SafeShell asks you first.\n")
	}
	return b.String()
}
