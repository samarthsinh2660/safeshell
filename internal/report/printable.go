package report

import (
	"fmt"
	"strings"
)

// Unsafe is true for a character that can change what a terminal shows:
// control characters, DEL, C1 controls, and invisible direction or format marks.
func Unsafe(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) ||
		(r >= 0x200b && r <= 0x200f) || (r >= 0x202a && r <= 0x202e) ||
		(r >= 0x2066 && r <= 0x2069) || r == 0xfeff
}

// ContainsUnsafe reports whether s has any Unsafe character.
func ContainsUnsafe(s string) bool {
	return strings.IndexFunc(s, Unsafe) >= 0
}

// Printable makes text that other people control safe to show in a terminal.
// Unsafe characters become a visible escape such as \x1b or ‮, and a tab
// becomes a space. Ordinary text in any language, and emoji, is left as it is.
func Printable(s string) string {
	if !ContainsUnsafe(s) {
		return s
	}
	var sb strings.Builder
	for _, r := range s {
		switch {
		case r == '\t':
			sb.WriteByte(' ')
		case r < 0x100 && Unsafe(r):
			fmt.Fprintf(&sb, `\x%02x`, r)
		case Unsafe(r):
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
