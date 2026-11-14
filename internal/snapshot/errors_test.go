package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// notRoot skips tests that rely on permissions, which root ignores.
func notRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
}

// readOnly makes dir unwritable for the rest of the test.
func readOnly(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
}

func TestCreateFailsWhenTheStoreCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	write(t, file, "x", 0o644)
	if _, err := Create(filepath.Join(file, "store"), "rm x", nil, 0); err == nil {
		t.Error("a store under a file cannot be created")
	}
}

func TestListFailsWhenTheStoreIsNotAFolder(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	write(t, file, "x", 0o644)
	if _, err := List(file); err == nil {
		t.Error("a file is not a store")
	}
}

func TestCopyBlobAndHashFileReportUnreadableFiles(t *testing.T) {
	snap := t.TempDir()
	if err := os.Mkdir(filepath.Join(snap, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir() // reading a folder as a file fails
	file := filepath.Join(dir, "f")
	write(t, file, "x", 0o644)
	if _, err := copyBlob(snap, filepath.Join(dir, "missing")); err == nil {
		t.Error("copyBlob: missing source")
	}
	if _, err := copyBlob(snap, dir); err == nil {
		t.Error("copyBlob: a folder cannot be copied")
	}
	if _, err := copyBlob(t.TempDir(), file); err == nil {
		t.Error("copyBlob: no blobs folder to write into")
	}
	if _, err := hashFile(filepath.Join(dir, "missing")); err == nil {
		t.Error("hashFile: missing file")
	}
	if _, err := hashFile(dir); err == nil {
		t.Error("hashFile: a folder cannot be hashed")
	}
}

func TestWriteBlobReportsEachFailure(t *testing.T) {
	dir := t.TempDir()
	blob := filepath.Join(dir, "blob")
	write(t, blob, "data", 0o644)
	e := Entry{Path: filepath.Join(dir, "out"), Mode: 0o644}
	if err := writeBlob(filepath.Join(dir, "missing"), e); err == nil {
		t.Error("missing blob")
	}
	if err := writeBlob(blob, Entry{Path: filepath.Join(dir, "no", "such", "out")}); err == nil {
		t.Error("destination folder missing")
	}
	if err := writeBlob(dir, e); err == nil { // a folder is not readable as a blob
		t.Error("unreadable blob")
	}
}

func TestRestoreEntryReportsEachFailure(t *testing.T) {
	notRoot(t)
	root := t.TempDir()
	file := filepath.Join(root, "file")
	write(t, file, "x", 0o644)

	if _, err := restoreEntry(root, "id", Entry{Path: file, Kind: "weird"}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown kind: %v", err)
	}
	// Nothing can be made under a regular file.
	under := filepath.Join(file, "x")
	if _, err := restoreEntry(root, "id", Entry{Path: under, Kind: Dir}); err == nil {
		t.Error("a folder under a file")
	}
	if _, err := restoreEntry(root, "id", Entry{Path: under, Kind: Symlink, Link: "t"}); err == nil {
		t.Error("a link under a file")
	}

	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(locked, "inner", "f"), "x", 0o644)
	write(t, filepath.Join(locked, "link"), "x", 0o644)
	if err := os.Symlink("elsewhere", filepath.Join(locked, "sym")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(locked, "wasfile"), "x", 0o644)
	readOnly(t, locked)
	readOnly(t, filepath.Join(locked, "inner"))
	// A file where a folder belongs cannot be replaced in a read-only folder.
	if _, err := restoreEntry(root, "id", Entry{Path: filepath.Join(locked, "wasfile"), Kind: Dir}); err == nil {
		t.Error("replace a file with a folder")
	}
	// A different link cannot be replaced either.
	if _, err := restoreEntry(root, "id", Entry{Path: filepath.Join(locked, "sym"), Kind: Symlink, Link: "other"}); err == nil {
		t.Error("replace a link")
	}
	// A folder where a file belongs cannot be removed.
	if _, err := restoreEntry(root, "id", Entry{Path: filepath.Join(locked, "inner"), Kind: File, SHA: "x"}); err == nil {
		t.Error("replace a folder with a file")
	}
	// A file that differs cannot be rewritten because no temporary file can be made.
	if _, err := restoreEntry(root, "id", Entry{Path: filepath.Join(locked, "link"), Kind: File, SHA: "none"}); err == nil {
		t.Error("write into a read-only folder")
	}
}

func TestRestoreReportsProblemsInsteadOfStopping(t *testing.T) {
	notRoot(t)
	root, store := t.TempDir(), t.TempDir()
	keep := filepath.Join(root, "keep")
	gone := filepath.Join(root, "gone")
	write(t, filepath.Join(gone, "f"), "x", 0o644)
	m, err := Create(store, "x", []Target{{Path: gone}, {Path: filepath.Join(keep, "made"), Create: true}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(keep, "made"), "x", 0o644) // created by the command
	// The created file cannot be removed (read-only folder), and the folder to
	// restore into is not writable either.
	readOnly(t, keep)
	r, err := Restore(store, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	var removeFailed bool
	for _, p := range r.Problems {
		removeFailed = removeFailed || strings.Contains(p, "could not remove")
	}
	if !removeFailed {
		t.Errorf("problems: %v", r.Problems)
	}
	if _, err := Restore(store, "20060102T150405.000000000Z"); err == nil {
		t.Error("a missing snapshot must be an error")
	}
}

func TestVerifyNamesEveryKindOfDifference(t *testing.T) {
	root := t.TempDir()
	notDir := filepath.Join(root, "notdir")
	write(t, notDir, "x", 0o644)
	link := filepath.Join(root, "link")
	if err := os.Symlink("a", link); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "dir")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := Manifest{Entries: []Entry{
		{Path: notDir, Kind: Dir, Mode: 0o644},
		{Path: link, Kind: Symlink, Link: "b"},
		{Path: dir, Kind: File, Mode: 0o755},
		{Path: filepath.Join(root, "absent"), Kind: Absent},
		{Path: notDir, Kind: Absent},
	}}
	got := strings.Join(Verify(root, m), "\n")
	for _, want := range []string{"is not a folder", "points somewhere else", "is not a regular file", "still exists but did not before"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
}
