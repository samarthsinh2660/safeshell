package report

import (
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func diffOf(cs ...sandbox.Change) *sandbox.FileDiff { return &sandbox.FileDiff{Changes: cs} }

func observed(l domain.Level) []domain.Effect {
	return []domain.Effect{{Kind: domain.EffectFS, Evidence: domain.Observed, MinLevel: l}}
}

func TestSaferAlternative(t *testing.T) {
	dir := sandbox.Change{Path: "home/me/data", Kind: sandbox.Deleted, IsDir: true}
	file := sandbox.Change{Path: "home/me/data/a.txt", Kind: sandbox.Modified}
	orig := Outcome{Level: domain.R3, Decision: domain.Block, Effects: observed(domain.R3), Diff: diffOf(dir)}
	alt := func(f func(*Outcome)) Outcome {
		o := Outcome{Level: domain.R1, Decision: domain.Allow, Effects: observed(domain.R1), Diff: diffOf(file)}
		f(&o)
		return o
	}
	modifiedDir := &Outcome{Level: domain.R3, Effects: observed(domain.R3), Diff: diffOf(sandbox.Change{Path: "home/me/data", Kind: sandbox.Modified, IsDir: true})}
	createdDir := &Outcome{Level: domain.R3, Effects: observed(domain.R3), Diff: diffOf(sandbox.Change{Path: "home/me/data", Kind: sandbox.Created, IsDir: true})}
	for _, c := range []struct {
		name string
		alt  Outcome
		orig *Outcome
		want bool
	}{
		{"valid", alt(func(*Outcome) {}), nil, true},
		{"changes nothing", alt(func(o *Outcome) { o.Diff = diffOf() }), nil, true},
		{"previewed evidence", alt(func(o *Outcome) { o.Effects = []domain.Effect{{Evidence: domain.Previewed}} }), nil, true},
		{"same level", alt(func(o *Outcome) { o.Level = domain.R3 }), nil, false},
		{"higher level", alt(func(o *Outcome) { o.Level = domain.R4 }), nil, false},
		{"hard block", alt(func(o *Outcome) { o.Decision = domain.HardBlock }), nil, false},
		{"team blocked", alt(func(o *Outcome) { o.TeamBlocked = true }), nil, false},
		{"estimated beside observed", alt(func(o *Outcome) { o.Effects = append(o.Effects, domain.Effect{Evidence: domain.Estimated}) }), nil, false},
		{"only inspected", alt(func(o *Outcome) { o.Effects = []domain.Effect{{Evidence: domain.Inspected}} }), nil, false},
		{"no evidence", alt(func(o *Outcome) { o.Effects = nil }), nil, false},
		{"alt not observed", alt(func(o *Outcome) { o.Diff = nil }), nil, false},
		{"original not observed", alt(func(*Outcome) {}), &Outcome{Level: domain.R3, Effects: observed(domain.R3)}, false},
		{"extra path", alt(func(o *Outcome) { o.Diff = diffOf(file, sandbox.Change{Path: "home/me/other", Kind: sandbox.Created}) }), nil, false},
		{"sibling with the same prefix", alt(func(o *Outcome) { o.Diff = diffOf(sandbox.Change{Path: "home/me/data2/x", Kind: sandbox.Created}) }), nil, false},
		{"same path, deleted as well", alt(func(o *Outcome) { o.Diff = diffOf(dir) }), nil, true},
		{"new deletion", alt(func(o *Outcome) { o.Diff = diffOf(sandbox.Change{Path: "home/me/data/a.txt", Kind: sandbox.Deleted}) }), modifiedDir, false},
		{"new replacement", alt(func(o *Outcome) {
			o.Diff = diffOf(sandbox.Change{Path: "home/me/data", Kind: sandbox.Replaced, IsDir: true})
		}), createdDir, false},
	} {
		o := orig
		if c.orig != nil {
			o = *c.orig
		}
		if got := SaferAlternative(o, c.alt); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
