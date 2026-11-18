package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOversizedTargetFailsBeforeAnythingIsWritten(t *testing.T) {
	root, _ := tree(t)
	store := filepath.Join(t.TempDir(), "never-made")
	_, err := Create(store, "x", []Target{{Path: filepath.Join(root, "proj")}}, 8)
	if !errors.Is(err, ErrTooLarge) || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("err %v", err)
	}
	if _, err := os.Stat(store); err == nil {
		t.Error("the store was touched before the size check")
	}
}

func TestPreflightChecksFreeSpace(t *testing.T) {
	root, _ := tree(t)
	store := filepath.Join(t.TempDir(), "s")
	ts := []Target{{Path: filepath.Join(root, "proj")}}
	space := func(free, total int64) func(string) (int64, int64, error) {
		return func(string) (int64, int64, error) { return free, total, nil }
	}
	if err := preflight(store, ts, 0, space(100<<20, 10<<30)); !errors.Is(err, ErrNoSpace) || !strings.Contains(err.Error(), "100.0 MiB is free") {
		t.Errorf("low space: %v", err)
	}
	if err := preflight(store, ts, 0, space(2<<30, 10<<30)); err != nil {
		t.Errorf("enough space: %v", err)
	}
	if err := preflight(store, ts, 0, func(string) (int64, int64, error) { return 0, 0, errors.New("no statfs") }); err != nil {
		t.Errorf("unknown space must not block: %v", err)
	}
}

func TestCheckSpaceReserve(t *testing.T) {
	for _, c := range []struct {
		size, free, total int64
		ok                bool
	}{
		{0, 2 << 30, 100 << 30, true},
		{1 << 30, 2<<30 - 1, 100 << 30, false}, // 1 GiB reserve on a big disk
		{1 << 30, 2 << 30, 100 << 30, true},
		{40 << 20, 100 << 20, 1 << 30, true},  // small disk: reserve is 5% (about 54 MiB)
		{50 << 20, 100 << 20, 1 << 30, false}, // 50 + 54 > 100
	} {
		if err := checkSpace(c.size, c.free, c.total); (err == nil) != c.ok {
			t.Errorf("%+v: %v", c, err)
		}
	}
}

func TestExistingParentAndDiskSpace(t *testing.T) {
	d := t.TempDir()
	if got := existingParent(filepath.Join(d, "a", "b")); got != d {
		t.Errorf("got %s", got)
	}
	if got := existingParent("/no-such-root-dir/x"); got != "/" {
		t.Errorf("got %s", got)
	}
	if free, total, err := diskSpace(d); err != nil || free <= 0 || total < free {
		t.Errorf("%d %d %v", free, total, err)
	}
	if _, _, err := diskSpace(filepath.Join(d, "missing")); err == nil {
		t.Error("expected an error")
	}
}

func TestEstimateCountsEachFileOnce(t *testing.T) {
	root, _ := tree(t)
	p := filepath.Join(root, "proj")
	size, over := estimate([]Target{{Path: p}, {Path: filepath.Join(p, "a.txt")}, {Path: filepath.Join(root, "missing")}}, 0)
	if over || size != int64(len("alpha")+len("#!/bin/sh")+len("gamma")) {
		t.Errorf("%d %v", size, over)
	}
	if _, over := estimate([]Target{{Path: p}}, 5); !over {
		t.Error("expected over")
	}
}

// fake stores a snapshot with a given age and size without copying anything.
func fake(t *testing.T, store, id string, age time.Duration, bytes int64) {
	t.Helper()
	if err := os.MkdirAll(dir(store, id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeManifest(store, Manifest{ID: id, Created: time.Now().Add(-age), Bytes: bytes}); err != nil {
		t.Fatal(err)
	}
}

func ids(t *testing.T, store string) []string {
	t.Helper()
	ms, _ := List(store)
	var out []string
	for _, m := range ms {
		out = append(out, m.ID)
	}
	return out
}

const (
	idA = "20250101T000000.000000000Z"
	idB = "20250102T000000.000000000Z"
	idC = "20250103T000000.000000000Z"
)

func TestPruneBySizeRemovesOldestFirstAndKeepsTheNewSnapshot(t *testing.T) {
	store := t.TempDir()
	fake(t, store, idA, 3*time.Hour, 100)
	fake(t, store, idB, 2*time.Hour, 100)
	fake(t, store, idC, time.Hour, 100)
	r, err := Prune(store, 0, 150, "")
	if err != nil || r.Removed != 2 || r.Left != 1 || r.Freed != 200 || r.Size != 100 {
		t.Fatalf("%+v %v", r, err)
	}
	if got := ids(t, store); len(got) != 1 || got[0] != idC {
		t.Errorf("left %v", got)
	}

	fake(t, store, idA, 3*time.Hour, 100) // the oldest is the one just taken: it stays
	r, _ = Prune(store, 0, 50, idA)
	if got := ids(t, store); len(got) != 1 || got[0] != idA || r.Removed != 1 {
		t.Errorf("left %v %+v", got, r)
	}
}

func TestPruneByAgeStopsAtTheFirstYoungSnapshot(t *testing.T) {
	store := t.TempDir()
	fake(t, store, idA, 40*24*time.Hour, 10)
	fake(t, store, idB, 35*24*time.Hour, 10)
	fake(t, store, idC, time.Hour, 10)
	r, err := Prune(store, 30*24*time.Hour, 1<<40, "")
	if err != nil || r.Removed != 2 || r.Left != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	if r, _ := Prune(store, 0, 0, ""); r.Removed != 0 { // no limits: nothing goes
		t.Errorf("%+v", r)
	}
}

func TestPruneRemovesOnlyNotesOfDeletedSnapshots(t *testing.T) {
	store := t.TempDir()
	fake(t, store, idA, time.Hour, 1)
	gone, live := filepath.Join(store, ".unsealed-111"), filepath.Join(store, ".unsealed-222")
	os.WriteFile(gone, []byte(idB), 0o600)
	os.WriteFile(live, []byte(idA), 0o600)
	os.Mkdir(filepath.Join(store, ".unsealed-333"), 0o700) // unreadable as a note: left alone
	if _, err := Prune(store, 0, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(gone); err == nil {
		t.Error("orphan note kept")
	}
	if _, err := os.Stat(live); err != nil {
		t.Error("live note removed")
	}
}

func TestPruneErrors(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o600)
	if _, err := Prune(f, 0, 1, ""); err == nil { // a file, not a folder
		t.Error("expected a list error")
	}
	if r, err := Prune(filepath.Join(t.TempDir(), "none"), 0, 1, ""); err != nil || r.Removed != 0 {
		t.Errorf("%+v %v", r, err)
	}
	store := t.TempDir()
	fake(t, store, idA, time.Hour, 10)
	fake(t, store, idB, time.Minute, 10)
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	os.Chmod(store, 0o500)
	defer os.Chmod(store, 0o700)
	if _, err := Prune(store, 0, 5, ""); err == nil {
		t.Error("expected a remove error")
	}
}
