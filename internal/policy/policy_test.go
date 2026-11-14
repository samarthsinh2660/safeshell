package policy

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func TestDecideByLevel(t *testing.T) {
	for l, want := range map[domain.Level]domain.Decision{
		domain.R0: domain.Allow, domain.R1: domain.Allow, domain.R2: domain.Confirm,
		domain.R3: domain.Block, domain.R4: domain.HardBlock,
	} {
		if got := Decide(l, nil); got != want {
			t.Errorf("%s -> %s, want %s", l, got, want)
		}
	}
}

func TestEstimatedEffectsNeverAutoRun(t *testing.T) {
	est := []domain.Effect{{Evidence: domain.Estimated}}
	if Decide(domain.R1, est) != domain.Confirm || Decide(domain.R0, est) != domain.Confirm {
		t.Error("an estimated effect must turn Allow into Confirm")
	}
	if Decide(domain.R3, est) != domain.Block {
		t.Error("estimated effects must not soften a block")
	}
	for _, ev := range []domain.Evidence{domain.Inspected, domain.Previewed, domain.Observed} {
		if Decide(domain.R1, []domain.Effect{{Evidence: ev}}) != domain.Allow {
			t.Errorf("%s evidence should not force a confirmation", ev)
		}
	}
}

func TestFinalLevelOnlyRaises(t *testing.T) {
	fx := []domain.Effect{{MinLevel: domain.R3}, {MinLevel: domain.R1}}
	if got := FinalLevel(domain.R1, fx); got != domain.R3 {
		t.Errorf("got %s, want R3", got)
	}
	if got := FinalLevel(domain.R4, fx); got != domain.R4 {
		t.Errorf("effects must never lower the level, got %s", got)
	}
	if FinalLevel(domain.R2, nil) != domain.R2 {
		t.Error("no effects leaves the level alone")
	}
}

func TestWorthReviewOnlyForR2AndR3(t *testing.T) {
	for l, want := range map[domain.Level]bool{domain.R0: false, domain.R1: false, domain.R2: true, domain.R3: true, domain.R4: false} {
		if got := WorthReview(l); got != want {
			t.Errorf("WorthReview(%s) = %v, want %v", l, got, want)
		}
	}
}
