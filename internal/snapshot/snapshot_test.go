package snapshot

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), mode); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tree(t *testing.T) (root, store string) {
	root, store = t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "proj/a.txt"), "alpha", 0o644)
	write(t, filepath.Join(root, "proj/sub/b.sh"), "#!/bin/sh", 0o755)
	write(t, filepath.Join(root, "proj/sub/deep/c.txt"), "gamma", 0o600)
	if err := os.Symlink("a.txt", filepath.Join(root, "proj/link")); err != nil {
		t.Fatal(err)
	}
	return
}

func TestRestoreBringsBackDeletedModifiedAndRemovesCreated(t *testing.T) {
	root, store := tree(t)
	proj := filepath.Join(root, "proj")
	created := filepath.Join(root, "new.txt")
	m, err := Create(store, "rm -rf proj", []Target{{Path: proj}, {Path: created, Create: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Bytes != int64(len("alpha#!/bin/shgamma")) {
		t.Errorf("bytes %d", m.Bytes)
	}

	os.RemoveAll(proj)
	write(t, created, "stray", 0o644)

	r, err := Restore(store, m.ID)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("restore: %v %v", err, r.Problems)
	}
	if r.Removed != 1 || r.Restored < 6 {
		t.Errorf("report %+v", r)
	}
	if read(t, filepath.Join(proj, "a.txt")) != "alpha" || read(t, filepath.Join(proj, "sub/deep/c.txt")) != "gamma" {
		t.Error("contents not restored")
	}
	if fi, _ := os.Stat(filepath.Join(proj, "sub/b.sh")); fi.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", fi.Mode())
	}
	if l, _ := os.Readlink(filepath.Join(proj, "link")); l != "a.txt" {
		t.Errorf("link %q", l)
	}
	if _, err := os.Stat(created); err == nil {
		t.Error("created file survived undo")
	}
}

func TestRestorePreservesModificationTimes(t *testing.T) {
	root, store := tree(t)
	f := filepath.Join(root, "proj/a.txt")
	old := time.Unix(1_600_000_000, 0)
	os.Chtimes(f, old, old)
	m, _ := Create(store, "x", []Target{{Path: f}}, 0)
	os.Remove(f)
	if r, _ := Restore(store, m.ID); len(r.Problems) != 0 {
		t.Fatal(r.Problems)
	}
	if fi, _ := os.Stat(f); !fi.ModTime().Equal(old) {
		t.Errorf("mtime %v", fi.ModTime())
	}
}

func TestRestoreOverwritesModifiedFileAndLeavesIdenticalOnesAlone(t *testing.T) {
	root, store := tree(t)
	a, b := filepath.Join(root, "proj/a.txt"), filepath.Join(root, "proj/sub/b.sh")
	m, _ := Create(store, "sed -i", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	write(t, a, "CHANGED", 0o644)
	r, _ := Restore(store, m.ID)
	if read(t, a) != "alpha" || len(r.Problems) != 0 {
		t.Errorf("a: %q %v", read(t, a), r.Problems)
	}
	if r.Restored != 1 {
		t.Errorf("only the changed file should be rewritten, got %d (b untouched: %v)", r.Restored, b)
	}
}

func TestVerifyReportsWhatDoesNotMatch(t *testing.T) {
	root, store := tree(t)
	m, _ := Create(store, "x", []Target{{Path: filepath.Join(root, "proj")}, {Path: filepath.Join(root, "ghost"), Create: true}}, 0)
	write(t, filepath.Join(root, "proj/a.txt"), "tampered", 0o644)
	os.Chmod(filepath.Join(root, "proj/sub/b.sh"), 0o600)
	os.Remove(filepath.Join(root, "proj/sub/deep/c.txt"))
	write(t, filepath.Join(root, "ghost"), "", 0o644)
	if got := Verify(store, m); len(got) != 4 {
		t.Errorf("want 4 problems, got %v", got)
	}
}

func TestSizeLimitStopsAndLeavesNothingBehind(t *testing.T) {
	root, store := tree(t)
	_, err := Create(store, "x", []Target{{Path: filepath.Join(root, "proj")}}, 8)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err %v", err)
	}
	if es, _ := os.ReadDir(store); len(es) != 0 {
		t.Error("partial snapshot left behind")
	}
}

func TestMissingTargetIsSkippedAndSpecialFilesRefused(t *testing.T) {
	root, store := tree(t)
	if _, err := Create(store, "x", []Target{{Path: filepath.Join(root, "nope")}}, 0); err != nil {
		t.Errorf("missing path: %v", err)
	}
	if _, err := Create(store, "x", []Target{{Path: "/dev/null"}}, 0); err == nil {
		t.Error("a device must not be snapshotted")
	}
}

func TestListNewestFirstAndLoad(t *testing.T) {
	root, store := tree(t)
	m1, _ := Create(store, "one", []Target{{Path: filepath.Join(root, "proj/a.txt")}}, 0)
	m2, _ := Create(store, "two", []Target{{Path: filepath.Join(root, "proj/a.txt")}}, 0)
	ms, err := List(store)
	if err != nil || len(ms) != 2 || ms[0].ID != m2.ID || ms[1].ID != m1.ID {
		t.Fatalf("%v %v", ms, err)
	}
	if got, _ := Load(store, m1.ID); got.Line != "one" {
		t.Error(got.Line)
	}
	if ms, err := List(filepath.Join(store, "none")); ms != nil || err != nil {
		t.Error("a missing store is an empty list")
	}
	if _, err := Restore(store, "nope"); err == nil {
		t.Error("unknown snapshot")
	}
}

func TestRestoreReplacesAFolderThatTookTheNameOfAFile(t *testing.T) {
	root, store := tree(t)
	a := filepath.Join(root, "proj/a.txt")
	m, _ := Create(store, "x", []Target{{Path: a}}, 0)
	os.Remove(a)
	os.MkdirAll(filepath.Join(a, "inner"), 0o755)
	if r, _ := Restore(store, m.ID); len(r.Problems) != 0 || read(t, a) != "alpha" {
		t.Errorf("%v", r.Problems)
	}
}

func TestRestoreRebuildsAReadOnlyFolderAndItsContents(t *testing.T) {
	root, store := tree(t)
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "inner.txt"), "keep", 0o444)
	os.Chmod(locked, 0o555) // no write bit: a naive restore cannot put files back into it
	defer os.Chmod(locked, 0o755)
	m, err := Create(store, "rm -rf locked", []Target{{Path: locked}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(locked, 0o755)
	os.RemoveAll(locked)
	r, err := Restore(store, m.ID)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("%v %v", err, r.Problems)
	}
	if fi, _ := os.Stat(locked); fi.Mode().Perm() != 0o555 {
		t.Errorf("mode %v", fi.Mode())
	}
	if read(t, filepath.Join(locked, "inner.txt")) != "keep" {
		t.Error("contents lost")
	}
}

func TestCreateTightensALooseStore(t *testing.T) {
	store := filepath.Join(t.TempDir(), "snaps")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(store, "ls", nil, 0); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(store); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestCreateSavesACreatedPathThatAlreadyExists(t *testing.T) {
	root, store := t.TempDir(), t.TempDir()
	late := filepath.Join(root, "late")
	write(t, filepath.Join(late, "f"), "keep me", 0o644)
	m, err := Create(store, "x", []Target{{Path: late, Create: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(late); err != nil {
		t.Fatal(err)
	}
	r, err := Restore(store, m.ID)
	if err != nil || len(r.Problems) != 0 || r.Removed != 0 {
		t.Fatalf("restore: %v %+v", err, r)
	}
	if got := read(t, filepath.Join(late, "f")); got != "keep me" {
		t.Errorf("contents %q", got)
	}
}

func TestLoadRejectsIdsOutsideTheStore(t *testing.T) {
	for _, id := range []string{"../x", "..", "", "a/b", "20060102T150405.000000000Z/../x", "nope"} {
		if _, err := Load(t.TempDir(), id); !errors.Is(err, ErrBadID) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestRestoreWillNotFollowASymlinkedParent(t *testing.T) {
	root, store := t.TempDir(), t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(outside, "victim"), "outside", 0o644)
	write(t, filepath.Join(outside, "dir/f"), "outside", 0o644)
	real := filepath.Join(root, "shared")
	write(t, filepath.Join(real, "made"), "x", 0o644)
	write(t, filepath.Join(real, "kept"), "a", 0o644)
	write(t, filepath.Join(real, "d/f"), "a", 0o644)
	m, err := Create(store, "x", []Target{{Path: filepath.Join(real, "kept")}, {Path: filepath.Join(real, "d")}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	m.Entries = append(m.Entries, Entry{Path: filepath.Join(real, "victim"), Kind: Absent})
	if err := os.RemoveAll(real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, real); err != nil {
		t.Fatal(err)
	}
	// Absent entry for the outside file, reached through the link.
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(store, m.ID, "manifest.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Restore(store, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Problems) == 0 || !strings.Contains(strings.Join(r.Problems, "\n"), "will not follow") {
		t.Errorf("problems: %v", r.Problems)
	}
	if read(t, filepath.Join(outside, "victim")) != "outside" || read(t, filepath.Join(outside, "dir/f")) != "outside" {
		t.Error("a file outside was touched")
	}
	if _, err := os.Lstat(filepath.Join(outside, "kept")); err == nil {
		t.Error("restore wrote through the link")
	}
}

func TestUndoWorksThroughALinkThatWasThereBeforeTheCommand(t *testing.T) {
	root, store := t.TempDir(), t.TempDir()
	real := filepath.Join(root, "disk", "projects")
	write(t, filepath.Join(real, "app/main.go"), "v1", 0o644)
	link := filepath.Join(root, "projects") // like ~/projects -> /data/projects, or /bin -> usr/bin
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	m, err := Create(store, "x", []Target{{Path: filepath.Join(link, "app/main.go")}, {Path: filepath.Join(link, "app/new/out.txt"), Create: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.Entries[0].Path != filepath.Join(real, "app/main.go") || m.Entries[1].Path != filepath.Join(real, "app/new/out.txt") {
		t.Fatalf("paths are not saved resolved: %+v", m.Entries)
	}
	write(t, filepath.Join(link, "app/main.go"), "v2", 0o644)
	write(t, filepath.Join(link, "app/new/out.txt"), "made", 0o644)
	r, err := Restore(store, m.ID)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("err=%v problems=%v", err, r.Problems)
	}
	if read(t, filepath.Join(real, "app/main.go")) != "v1" {
		t.Error("main.go was not restored")
	}
	if _, err := os.Lstat(filepath.Join(real, "app/new/out.txt")); err == nil {
		t.Error("the created file was not removed")
	}
}

func TestCreateDoesNotMarkAPathItCannotCheckAsAbsent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read every folder")
	}
	root, store := t.TempDir(), t.TempDir()
	locked := filepath.Join(root, "locked")
	write(t, filepath.Join(locked, "f"), "keep", 0o644)
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	m, _ := Create(store, "x", []Target{{Path: filepath.Join(locked, "f"), Create: true}}, 0)
	for _, e := range m.Entries {
		if e.Kind == Absent {
			t.Fatalf("a path that could not be checked was saved as absent: %+v", e)
		}
	}
}

func TestNoSymlinkParentsAllowsFoldersThatAreGone(t *testing.T) {
	if err := noSymlinkParents(filepath.Join(t.TempDir(), "gone", "deeper", "f")); err != nil {
		t.Error(err)
	}
}
