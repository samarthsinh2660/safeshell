//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiskStoreIsPrivateAndSweepsLeftovers(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if DiskBase() != filepath.Join(cache, "safeshell", "sandbox") {
		t.Fatalf("base %s", DiskBase())
	}
	left := filepath.Join(DiskBase(), "ss-sim-2147483647-old") // its owner pid is gone
	if err := os.MkdirAll(filepath.Join(left, "layers"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(DiskBase(), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := diskStore(os.Getuid(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(store, DiskBase()+string(filepath.Separator)+"ss-sim-") {
		t.Errorf("store %s", store)
	}
	if fi, _ := os.Stat(DiskBase()); fi.Mode().Perm() != 0o700 {
		t.Errorf("base mode %v, want 0700", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(store); fi.Mode().Perm() != 0o700 {
		t.Errorf("store mode %v, want 0700", fi.Mode().Perm())
	}
	if _, err := os.Stat(left); !os.IsNotExist(err) {
		t.Error("the folder of a dead run was not removed")
	}
	cleanup(store)
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Error("cleanup left the store")
	}
}

func TestDiskStoreRefusesWhatIsNotYoursOrNotAFolder(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if err := os.MkdirAll(filepath.Join(cache, "safeshell"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, DiskBase()); err != nil {
		t.Fatal(err)
	}
	if _, err := diskStore(os.Getuid(), time.Now()); err == nil || !strings.Contains(err.Error(), "not your own folder") {
		t.Errorf("a symlink as the base: %v", err)
	}
	os.Remove(DiskBase())
	if _, err := diskStore(os.Getuid()+1, time.Now()); err == nil || !strings.Contains(err.Error(), "not your own folder") {
		t.Errorf("a folder of someone else: %v", err)
	}

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CACHE_HOME", file)
	if _, err := diskStore(os.Getuid(), time.Now()); err == nil || !strings.Contains(err.Error(), "for a large run") {
		t.Errorf("a base that cannot be made: %v", err)
	}
}
