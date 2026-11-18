package main

import (
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

func TestParseAgeAndSize(t *testing.T) {
	for in, want := range map[string]time.Duration{"30d": 30 * 24 * time.Hour, "12h": 12 * time.Hour} {
		if got, ok := parseAge(in); !ok || got != want {
			t.Errorf("age %q: %v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "30", "d", "0d", "-1d", "1dh", "x"} {
		if _, ok := parseAge(bad); ok {
			t.Errorf("age %q accepted", bad)
		}
	}
	for in, want := range map[string]int64{"5G": 5 << 30, "500m": 500 << 20, "2GiB": 2 << 30, "10k": 10 << 10, "1T": 1 << 40, "7": 7, "3B": 3} {
		if got, ok := parseSize(in); !ok || got != want {
			t.Errorf("size %q: %v %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "G", "0", "-5G", "abc", "99999999999T"} {
		if _, ok := parseSize(bad); ok {
			t.Errorf("size %q accepted", bad)
		}
	}
}

// storeWith saves n snapshots of the given file under the test store.
func storeWith(t *testing.T, n int) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(f, []byte("0123456789"), 0o600)
	for i := 0; i < n; i++ {
		if _, err := snapshot.Create(snapshotDir(), "rm f", []snapshot.Target{{Path: f}}, 0); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotsPruneFlagsAndErrors(t *testing.T) {
	useStore(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	storeWith(t, 3)
	for _, args := range [][]string{{"--older"}, {"--max"}, {"--bogus", "1"}, {"5G"}, {"--older", "soon"}, {"--max", "lots"}} {
		if code, _, errOut := runSS(append([]string{"snapshots", "prune"}, args...)...); code != 2 || !strings.Contains(errOut, "safeshell snapshots prune") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	if ms, _ := snapshot.List(snapshotDir()); len(ms) != 3 {
		t.Fatalf("bad flags deleted snapshots: %d left", len(ms))
	}
	code, out, _ := runSS("snapshots", "prune", "--older", "30d", "--max", "15")
	if code != 0 || !strings.Contains(out, "Removed 2") || !strings.Contains(out, "1 left") {
		t.Errorf("%d %q", code, out)
	}
	if code, out, _ := runSS("snapshots", "prune"); code != 0 || !strings.Contains(out, "Removed 0") {
		t.Errorf("defaults: %d %q", code, out)
	}
}

func TestSnapshotsPruneFailsWhenTheStoreCannotBeRead(t *testing.T) {
	old := snapshotDir
	f := filepath.Join(t.TempDir(), "file")
	os.WriteFile(f, nil, 0o600)
	snapshotDir = func() string { return f }
	t.Cleanup(func() { snapshotDir = old })
	if code, _, errOut := runSS("snapshots", "prune"); code != 1 || !strings.Contains(errOut, "safeshell snapshots prune:") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestSnapshotsListShowsTotal(t *testing.T) {
	useStore(t)
	storeWith(t, 2)
	if _, out, _ := runSS("snapshots"); !strings.Contains(out, "2 saved, 20 B in all") {
		t.Errorf("%q", out)
	}
}

func TestPruneLimitsFromConfigAndAutoPrune(t *testing.T) {
	useStore(t)
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	if age, max := pruneLimits(); age != 30*24*time.Hour || max != 5<<30 {
		t.Errorf("defaults %v %d", age, max)
	}
	os.MkdirAll(filepath.Join(cfg, "safeshell"), 0o700)
	os.WriteFile(filepath.Join(cfg, "safeshell", "config.json"), []byte(`{"snapshots":{"keep_days":2,"max_gib":1}}`), 0o600)
	if age, max := pruneLimits(); age != 48*time.Hour || max != 1<<30 {
		t.Errorf("configured %v %d", age, max)
	}
	os.WriteFile(filepath.Join(cfg, "safeshell", "config.json"), []byte(`{bad`), 0o600)
	if age, _ := pruneLimits(); age != 30*24*time.Hour {
		t.Errorf("a broken config must fall back to defaults, got %v", age)
	}
}

func TestNewSnapshotPrunesOldOnesButNotItself(t *testing.T) {
	useStore(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	old := "20200101T000000.000000000Z"
	os.MkdirAll(filepath.Join(snapshotDir(), old), 0o700)
	os.WriteFile(filepath.Join(snapshotDir(), old, "manifest.json"), []byte(`{"id":"`+old+`","created":"2020-01-01T00:00:00Z"}`), 0o600)
	f := filepath.Join(t.TempDir(), "f.txt")
	os.WriteFile(f, []byte("x"), 0o600)
	a := sampleAnalysis()
	a.Sim = &sandbox.Result{Diff: sandbox.FileDiff{Changes: []sandbox.Change{{Path: strings.TrimPrefix(f, "/"), Kind: sandbox.Deleted}}}}
	if note := protect(a, "/"); !strings.Contains(note, "Snapshot saved") {
		t.Fatal(note)
	}
	ms, _ := snapshot.List(snapshotDir())
	if len(ms) != 1 || ms[0].ID == old {
		t.Errorf("want only the new snapshot, got %d", len(ms))
	}
}
