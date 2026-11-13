package xdg

import "testing"

func TestDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	if got := Dir("XDG_DATA_HOME", ".local", "share"); got != "/data/safeshell" {
		t.Error(got)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", "/home/alice")
	if got := Dir("XDG_DATA_HOME", ".local", "share"); got != "/home/alice/.local/share/safeshell" {
		t.Error(got)
	}
	if got := Dir("XDG_DATA_HOME"); got != "/home/alice/safeshell" {
		t.Error("no fallback folders:", got)
	}
}
