package domain

import (
	"encoding/json"
	"testing"
)

func TestLevelNames(t *testing.T) {
	want := []struct{ s, label string }{{"R0", "Read-only"}, {"R1", "Reversible"}, {"R2", "Recoverable"}, {"R3", "Irreversible"}, {"R4", "Catastrophic"}}
	for i, w := range want {
		l := Level(i)
		if l.String() != w.s || l.Label() != w.label {
			t.Errorf("level %d = %s/%s, want %s/%s", i, l, l.Label(), w.s, w.label)
		}
	}
}

func TestLevelJSON(t *testing.T) {
	b, _ := json.Marshal(R3)
	if string(b) != `"R3"` {
		t.Errorf("got %s", b)
	}
}

func TestMaxLevel(t *testing.T) {
	if MaxLevel(nil) != R0 {
		t.Error("no findings should be R0")
	}
	fs := []Finding{{Level: R1}, {Level: R4}, {Level: R2}}
	if MaxLevel(fs) != R4 {
		t.Error("want R4")
	}
}

func TestWithAltDoesNotMutateOriginal(t *testing.T) {
	f := Finding{Level: R3}
	g := f.WithAlt("trash-put x", "recoverable")
	if f.Alt != nil || g.Alt == nil || g.Alt.Command != "trash-put x" {
		t.Errorf("WithAlt must return a copy: %+v %+v", f, g)
	}
}

func TestEvidenceAndDecisionNames(t *testing.T) {
	if Observed.String() != "OBSERVED" || Estimated.String() != "ESTIMATED" || Inspected.String() != "INSPECTED" || Previewed.String() != "PREVIEWED" {
		t.Error("evidence names")
	}
	if Allow.String() != "allow" || HardBlock.String() != "hard-block" {
		t.Error("decision names")
	}
	b, _ := json.Marshal(Effect{Kind: EffectFS, Evidence: Observed})
	if string(b) != `{"kind":"files","evidence":"OBSERVED","summary":"","min_level":"R0"}` {
		t.Errorf("effect JSON = %s", b)
	}
	d, _ := json.Marshal(Confirm)
	if string(d) != `"confirm"` {
		t.Errorf("decision JSON = %s", d)
	}
	if !(Observed > Previewed && Previewed > Inspected && Inspected > Estimated) {
		t.Error("evidence must be ordered strongest last")
	}
}

func TestParseLevel(t *testing.T) {
	for i, s := range []string{"R0", "R1", "R2", "R3", "R4"} {
		if l, ok := ParseLevel(s); !ok || l != Level(i) {
			t.Errorf("ParseLevel(%q) = %v, %v", s, l, ok)
		}
	}
	for _, s := range []string{"", "r3", "R5", " R3", "3"} {
		if _, ok := ParseLevel(s); ok {
			t.Errorf("ParseLevel(%q) accepted", s)
		}
	}
}

func TestExitCodes(t *testing.T) {
	levels := []struct {
		l    Level
		want int
	}{{R0, 0}, {R1, 0}, {R2, 0}, {R3, 3}, {R4, 4}}
	for _, c := range levels {
		if got := c.l.ExitCode(); got != c.want {
			t.Errorf("%s.ExitCode() = %d, want %d", c.l, got, c.want)
		}
	}
	decisions := []struct {
		d    Decision
		want int
	}{{Allow, 0}, {Confirm, 0}, {Block, 3}, {HardBlock, 4}}
	for _, c := range decisions {
		if got := c.d.ExitCode(); got != c.want {
			t.Errorf("%s.ExitCode() = %d, want %d", c.d, got, c.want)
		}
	}
}

func TestUnmarshalRejectsNonString(t *testing.T) {
	var l Level
	var e Evidence
	var d Decision
	for _, v := range []any{&l, &e, &d} {
		if json.Unmarshal([]byte(`3`), v) == nil {
			t.Errorf("a number was accepted as %T", v)
		}
	}
}
