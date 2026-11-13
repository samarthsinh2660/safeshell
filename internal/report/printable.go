package report

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Unsafe is true for a character that can change what a terminal shows or hide
// what a command says: control characters (C0, DEL, C1), format characters such
// as direction marks and zero-width characters, line and paragraph separators,
// and the blank Hangul filler letters.
func Unsafe(r rune) bool {
	switch r {
	case 0x115f, 0x1160, 0x3164, 0xffa0:
		return true
	}
	return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp)
}

// ContainsUnsafe reports whether s has an Unsafe character or is not valid UTF-8.
func ContainsUnsafe(s string) bool {
	return !utf8.ValidString(s) || strings.IndexFunc(s, Unsafe) >= 0
}

// Printable makes text that other people control safe to show in a terminal.
// Unsafe characters and invalid bytes become a visible escape such as \x1b,
// ‮ or \U000e0041, and a tab becomes a space. The joiners U+200C and U+200D
// are kept because emoji and many scripts need them. Ordinary text in any
// language, and emoji, is left as it is.
func Printable(s string) string {
	if utf8.ValidString(s) && strings.IndexFunc(s, replaced) < 0 {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&sb, `\x%02x`, s[i])
		case r == '\t':
			sb.WriteByte(' ')
		case !replaced(r):
			sb.WriteString(s[i : i+n])
		case r < 0x100:
			fmt.Fprintf(&sb, `\x%02x`, r)
		case r <= 0xffff:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			fmt.Fprintf(&sb, `\U%08x`, r)
		}
		i += n
	}
	return sb.String()
}

// replaced is true for a character Printable must change: tab, and every Unsafe one except the joiners.
func replaced(r rune) bool {
	return r == '\t' || (Unsafe(r) && r != 0x200c && r != 0x200d)
}
