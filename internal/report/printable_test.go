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
		{"lone c1 byte", "a\x9bb", `a\x9bb`},
		{"invalid utf8", "\xff", `\xff`},
		{"arabic letter mark", "a\u061cb", `a\u061cb`},
		{"line separator", "a\u2028b", `a\u2028b`},
		{"soft hyphen", "a\u00adb", `a\xadb`},
		{"tag character", "a\U000e0041b", `a\U000e0041b`},
		{"hangul filler", "a\u3164b", `a\u3164b`},
		{"emoji zwj sequence", "\U0001f468\u200d\U0001f469", "\U0001f468\u200d\U0001f469"},
		{"devanagari zwnj", "क\u200cष", "क\u200cष"},
	} {
		if got := Printable(c.in); got != c.want {
			t.Errorf("%s: Printable(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestContainsUnsafe(t *testing.T) {
	for in, want := range map[string]bool{"ls -la": false, "नमस्ते": false, "a\xffb": true, "a\x9bb": true, "a\u200db": true, "a\u2028b": true} {
		if got := ContainsUnsafe(in); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
}
