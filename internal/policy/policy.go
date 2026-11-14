// Package policy decides what happens at each risk level. It is pure.
package policy

import "github.com/samarthsinh2660/safeshell/internal/domain"

// Decide maps a final level to a decision. A command with any effect we could
// only estimate is never allowed to run unattended.
func Decide(level domain.Level, effects []domain.Effect) domain.Decision {
	d := domain.Allow
	switch level {
	case domain.R2:
		d = domain.Confirm
	case domain.R3:
		d = domain.Block
	case domain.R4:
		d = domain.HardBlock
	}
	if d == domain.Allow {
		for _, e := range effects {
			if e.Evidence == domain.Estimated {
				return domain.Confirm
			}
		}
	}
	return d
}

// WorthReview is true for levels where an AI review can still change the
// outcome and is worth its delay: R2 and R3. Below R2 nothing is asked; at R4
// the answer is already the strictest.
func WorthReview(level domain.Level) bool {
	return level >= domain.R2 && level < domain.R4
}

// FinalLevel is the highest of the rule level and what any effect justifies.
func FinalLevel(rule domain.Level, effects []domain.Effect) domain.Level {
	l := rule
	for _, e := range effects {
		l = max(l, e.MinLevel)
	}
	return l
}
