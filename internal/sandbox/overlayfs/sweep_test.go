//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"strconv"
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
	// The pid in a name means nothing: only the lock decides, so a folder named
	// for a dead pid is kept while locked, and one named for ours is removed
	// once its lock is free (as when the owner lives in another PID namespace).
	dead := 1 << 30
	gone := mk("ss-sim-" + strconv.Itoa(os.Getpid()) + "-1")
	held := mk("ss-sim-" + strconv.Itoa(dead) + "-1")
	unlocked, err := lockFolder(gone)
	if err != nil {
		t.Fatal(err)
	}
	unlocked.Close()
	owner, err := lockFolder(held) // a second open file description, like another process
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
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

	for p, wantGone := range map[string]bool{gone: true, oldLegacy: true, held: false, newLegacy: false, badPid: false, other: false, file: false} {
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

func TestSweepStaleKeepsAFreshFolderWithoutALockFile(t *testing.T) {
	base := t.TempDir()
	p := filepath.Join(base, "ss-sim-1-1") // a run that has made its folder but not yet locked it
	os.Mkdir(p, 0o755)
	sweepStale(base, os.Getuid(), time.Now())
	if _, err := os.Lstat(p); err != nil {
		t.Error("a folder that is not yet locked was removed")
	}
}

func TestLockFolderIsExclusiveUntilClosed(t *testing.T) {
	dir := t.TempDir()
	a, err := lockFolder(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockFolder(dir); err == nil {
		t.Error("a second lock on a locked folder succeeded")
	}
	if f, locked := tryLock(dir); locked {
		t.Error("tryLock took a held lock")
	} else {
		f.Close()
	}
	a.Close()
	f, locked := tryLock(dir)
	if !locked {
		t.Fatal("tryLock could not take a free lock")
	}
	f.Close()
	if _, err := lockFolder(filepath.Join(dir, "missing")); err == nil {
		t.Error("locking a folder that is not there succeeded")
	}
	if f, locked := tryLock(filepath.Join(dir, "missing")); f != nil || locked {
		t.Error("tryLock found a lock file in a missing folder")
	}
}
