// Package xdg finds SafeShell's folders under the XDG base directories. It
// imports nothing but the standard library, so any package may use it.
package xdg

import (
	"os"
	"path/filepath"
)

// Dir is SafeShell's folder under an XDG base directory: $envVar/safeshell
// when the variable is set, otherwise ~/<fallback...>/safeshell.
func Dir(envVar string, fallback ...string) string {
	if d := os.Getenv(envVar); d != "" {
		return filepath.Join(d, "safeshell")
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(append(append([]string{h}, fallback...), "safeshell")...)
}
