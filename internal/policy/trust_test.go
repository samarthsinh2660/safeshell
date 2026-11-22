package policy

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func observed(min domain.Level) []domain.Effect {
	return []domain.Effect{{Kind: domain.EffectFS, Evidence: domain.Observed, MinLevel: min}}
}

func TestNewTrustKeepsTheLineExactlyAndCleansTheFolder(t *testing.T) {
	got := NewTrust("  make   clean \n", "/home/me/app/../app/")
	if got != (Trust{Line: "make   clean", Dir: "/home/me/app"}) {
		t.Errorf("%+v", got)
	}
}

func TestTrustOnlyTurnsAPlainObservedR2ConfirmIntoAllow(t *testing.T) {
	set := []Trust{{Line: "make clean", Dir: "/app"}}
	base := func() TrustCase {
		return TrustCase{Set: set, Line: "make clean", Cwd: "/app", RuleLevel: domain.R2, Level: domain.R2, Effects: observed(domain.R2)}
	}
	with := func(f func(*TrustCase)) TrustCase { c := base(); f(&c); return c }
	for _, tc := range []struct {
		name string
		d    domain.Decision
		c    TrustCase
		want domain.Decision
	}{
		{"the plain case is allowed", domain.Confirm, base(), domain.Allow},
		{"previewed counts", domain.Confirm, with(func(c *TrustCase) { c.Effects[0].Evidence = domain.Previewed }), domain.Allow},
		{"spaces round the line do not matter", domain.Confirm, with(func(c *TrustCase) { c.Line = " make clean " }), domain.Allow},
		{"the folder is compared cleaned", domain.Confirm, with(func(c *TrustCase) { c.Cwd = "/app/" }), domain.Allow},
		{"R3 stays blocked", domain.Block, with(func(c *TrustCase) { c.RuleLevel, c.Level = domain.R3, domain.R3 }), domain.Block},
		{"R3 is not turned into Allow even from Confirm", domain.Confirm, with(func(c *TrustCase) { c.RuleLevel, c.Level = domain.R3, domain.R3 }), domain.Confirm},
		{"R4 stays a hard block", domain.HardBlock, with(func(c *TrustCase) { c.RuleLevel, c.Level = domain.R4, domain.R4 }), domain.HardBlock},
		{"R1 is already allowed", domain.Allow, with(func(c *TrustCase) { c.RuleLevel, c.Level = domain.R1, domain.R1 }), domain.Allow},
		{"estimated stays a confirmation", domain.Confirm, with(func(c *TrustCase) { c.Effects[0].Evidence = domain.Estimated }), domain.Confirm},
		{"inspected stays a confirmation", domain.Confirm, with(func(c *TrustCase) { c.Effects[0].Evidence = domain.Inspected }), domain.Confirm},
		{"one estimate among observed effects", domain.Confirm, with(func(c *TrustCase) {
			c.Effects = append(observed(domain.R2), domain.Effect{Evidence: domain.Estimated})
		}), domain.Confirm},
		{"no evidence at all", domain.Confirm, with(func(c *TrustCase) { c.Effects = nil }), domain.Confirm},
		{"another folder", domain.Confirm, with(func(c *TrustCase) { c.Cwd = "/app/sub" }), domain.Confirm},
		{"a folder that only starts the same", domain.Confirm, with(func(c *TrustCase) { c.Cwd = "/app2" }), domain.Confirm},
		{"another line", domain.Confirm, with(func(c *TrustCase) { c.Line = "make clean all" }), domain.Confirm},
		{"a line with an added command", domain.Confirm, with(func(c *TrustCase) { c.Line = "make clean; rm -rf x" }), domain.Confirm},
		{"raised to R3 by an effect or the AI", domain.Confirm, with(func(c *TrustCase) { c.Level = domain.R3 }), domain.Confirm},
		{"raised from R1 to R2 by an effect", domain.Confirm, with(func(c *TrustCase) { c.RuleLevel = domain.R1 }), domain.Confirm},
		{"flagged by a team rule, a protected folder, a prod host or the AI", domain.Confirm, with(func(c *TrustCase) { c.Flagged = true }), domain.Confirm},
		{"nothing trusted", domain.Confirm, with(func(c *TrustCase) { c.Set = nil }), domain.Confirm},
		{"a pattern is not a match", domain.Confirm, with(func(c *TrustCase) { c.Set = []Trust{{Line: "make *", Dir: "/app"}} }), domain.Confirm},
		{"a folder pattern is not a match", domain.Confirm, with(func(c *TrustCase) { c.Set = []Trust{{Line: "make clean", Dir: "/*"}} }), domain.Confirm},
	} {
		if got := DecideTrusted(tc.d, tc.c); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestAUnicodeSpaceAtTheEndMakesADifferentLine(t *testing.T) {
	if got := NewTrust("rm build/x.o\u00a0", "/w").Line; got != "rm build/x.o\u00a0" {
		t.Errorf("%q: a no-break space is part of the last word", got)
	}
	// DecideTrusted trims the line the same way NewTrust does.
	if NewTrust("rm build/x.o\u00a0", "/w") == NewTrust("rm build/x.o", "/w") {
		t.Error("a line ending in a no-break space is the same as the trusted line")
	}
}
