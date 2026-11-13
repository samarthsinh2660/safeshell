package report

import "testing"

func TestPrintable(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"plain", "ls -la /tmp", "ls -la /tmp"},
		{"hindi", "नमस्ते दुनिया", "नमस्ते दुनिया"},
		{"emoji", "party 🎉 time", "party 🎉 time"},
		{"escape sequence", "\x1b[2Kecho hi", `\x1b[2Kecho hi`},
		{"osc 52", "\x1b]52;c;ZXZpbA==\x07", `\x1b]52;c;ZXZpbA==\x07`},
		{"c1", "a\u009bb\u0085", `a\x9bb\x85`},
		{"bidi override", "evil\u202etxt.sh", `evil\u202etxt.sh`},
		{"zero width and bom", "a\u200bb\ufeffc\u2066", `a\u200bb\ufeffc\u2066`},
		{"tab", "a\tb", "a b"},
		{"newline and delete", "a\nb\x7f", `a\x0ab\x7f`},
	} {
		if got := Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}
