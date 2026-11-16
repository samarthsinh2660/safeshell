//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSweepStaleRemovesOnlyOurAbandonedFolders(t *testing.T) {
	base := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(base, name)
		if err := os.MkdirAll(filepath.Join(p, "work"), 0o755); err != nil {
			t.Fatal(err)
		}
		os.Chmod(filepath.Join(p, "work"), 0o000) // overlay work directories are mode 000
		return p
	}
	dead := deadPid(t)
	gone := mk("ss-sim-" + strconv.Itoa(dead) + "-1")
	alive := mk("ss-sim-" + strconv.Itoa(os.Getpid()) + "-1")
	oldLegacy := mk("ss-sim-123456")
	newLegacy := mk("ss-sim-654321")
	badPid := mk("ss-sim-x-1")
	other := mk("keep-me")
	file := filepath.Join(base, "ss-sim-"+strconv.Itoa(dead)+"-2")
	os.WriteFile(file, nil, 0o600)
	target := t.TempDir()
	os.WriteFile(filepath.Join(target, "precious"), nil, 0o600)
	link := filepath.Join(base, "ss-sim-"+strconv.Itoa(dead)+"-3")
	os.Symlink(target, link)
	now := time.Now()
	os.Chtimes(oldLegacy, now.Add(-2*time.Hour), now.Add(-2*time.Hour))

	sweepStale(base, os.Getuid(), now)

	for p, wantGone := range map[string]bool{gone: true, oldLegacy: true, alive: false, newLegacy: false, badPid: false, other: false, file: false} {
		if _, err := os.Lstat(p); (err != nil) != wantGone {
			t.Errorf("%s: gone=%v, want %v", filepath.Base(p), err != nil, wantGone)
		}
	}
	if _, err := os.Lstat(link); err != nil {
		t.Error("a symlink must be left alone")
	}
	if _, err := os.Stat(filepath.Join(target, "precious")); err != nil {
		t.Error("the sweep followed a symlink")
	}
}

func TestSweepStaleIgnoresAMissingBase(t *testing.T) {
	sweepStale("/definitely/not/here", os.Getuid(), time.Now())
}

func TestSweepStaleSkipsFoldersOfOtherUsers(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "ss-sim-999999999-1") // abandoned, but not owned by the uid sweeping
	os.Mkdir(p, 0o755)
	sweepStale(base, os.Getuid()+1, time.Now())
	if _, err := os.Lstat(p); err != nil {
		t.Error("another user's folder was removed")
	}
}

// deadPid is a pid no process can have: one above the kernel's pid_max. The next
// free pid would race with other test processes starting in parallel.
func deadPid(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Skip("no /proc/sys/kernel/pid_max")
	}
	max, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return max + 1
}
