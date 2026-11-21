//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func putFile(t *testing.T, p, s string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
}

func TestStageCopiesSmallFilesAndLeavesTheRealOnesAlone(t *testing.T) {
	real, merged := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(merged, real), 0o755); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(1_600_000_000, 0)
	putFile(t, filepath.Join(real, "a.txt"), "aaa", 0o640)
	if err := os.Chtimes(filepath.Join(real, "a.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(real, "big.bin"), strings.Repeat("x", maxLooseFile+1), 0o644)
	putFile(t, filepath.Join(real, "locked"), "secret", 0o000)
	_ = os.Mkdir(filepath.Join(real, "sub"), 0o755)
	_ = os.Symlink("a.txt", filepath.Join(real, "link"))
	if err := syscall.Mkfifo(filepath.Join(real, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(real)

	var c looseCopies
	skipped := c.stage(merged, real, entries)

	want := 3 // big.bin, pipe and locked (unreadable unless we are root)
	if os.Geteuid() == 0 {
		want = 2
	}
	if skipped != want {
		t.Errorf("skipped %d, want %d", skipped, want)
	}
	fi, err := os.Stat(filepath.Join(merged, real, "a.txt"))
	if err != nil || fi.Mode().Perm() != 0o640 || !fi.ModTime().Equal(when) {
		t.Errorf("copy not faithful: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(real, "a.txt")); string(b) != "aaa" {
		t.Error("the real file changed")
	}
	if len(c.dirs) != 1 || c.before[filepath.Join(real, "a.txt")].Size != 3 {
		t.Errorf("bookkeeping wrong: %+v", c)
	}
}

func TestStageWithNothingToCopyRecordsNothing(t *testing.T) {
	var c looseCopies
	if c.stage(t.TempDir(), "/x", nil) != 0 || len(c.before) != 0 || c.used != 0 {
		t.Errorf("nothing to copy, got %+v", c)
	}
	if len(c.dirs) != 1 {
		t.Errorf("an empty partly mirrored folder is still compared after the run, got %+v", c.dirs)
	}
}

func TestStageStopsAtTheTotalLimit(t *testing.T) {
	real, merged := t.TempDir(), t.TempDir()
	_ = os.MkdirAll(filepath.Join(merged, real), 0o755)
	putFile(t, filepath.Join(real, "a"), "aaa", 0o644)
	entries, _ := os.ReadDir(real)
	c := looseCopies{used: maxLooseTotal - 2}
	if c.stage(merged, real, entries) != 1 || len(c.before) != 0 {
		t.Errorf("a file past the total limit stays flagged, got %+v", c)
	}
}

func TestStageSkipsAFileThatCannotBeWritten(t *testing.T) {
	real := t.TempDir()
	putFile(t, filepath.Join(real, "a.txt"), "aaa", 0o644)
	entries, _ := os.ReadDir(real)
	var c looseCopies
	if c.stage(filepath.Join(real, "missing"), real, entries) != 1 || len(c.before) != 0 {
		t.Errorf("an uncopyable file stays flagged, got %+v", c)
	}
}

func TestCopyLooseMissingSource(t *testing.T) {
	if _, ok := copyLoose(filepath.Join(t.TempDir(), "nope"), filepath.Join(t.TempDir(), "x")); ok {
		t.Error("a missing source cannot be copied")
	}
}

func TestChangesReportsWhatTheCommandDidToCopies(t *testing.T) {
	d := t.TempDir()
	putFile(t, filepath.Join(d, "keep"), "k", 0o644)
	putFile(t, filepath.Join(d, "edit"), "old", 0o644)
	putFile(t, filepath.Join(d, "gone"), "g", 0o644)
	entries, _ := os.ReadDir(d)
	// Stage into a scratch tree, then treat that tree as the sandbox's root.
	merged := t.TempDir()
	root := filepath.Join(merged, d)
	_ = os.MkdirAll(root, 0o755)
	var c looseCopies
	c.stage(merged, d, entries)
	c.dirs = []string{root}
	before := c.before
	c.before = map[string]fileState{}
	for p, s := range before {
		c.before[filepath.Join(root, filepath.Base(p))] = s
	}
	if got := c.changes(); len(got) != 0 {
		t.Fatalf("untouched copies must show no change, got %+v", got)
	}

	putFile(t, filepath.Join(root, "edit"), "newer", 0o644)
	_ = os.Remove(filepath.Join(root, "gone"))
	putFile(t, filepath.Join(root, "fresh"), "f", 0o644)
	_ = os.Mkdir(filepath.Join(root, "dir"), 0o755)
	_ = os.Symlink("keep", filepath.Join(root, "link"))
	_ = syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644)

	got := map[string]sandbox.Kind{}
	for _, ch := range c.changes() {
		got[filepath.Base(ch.Path)] = ch.Kind
	}
	want := map[string]sandbox.Kind{"edit": sandbox.Modified, "gone": sandbox.Deleted, "fresh": sandbox.Created}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v want %v", k, got[k], v)
		}
	}
}

func TestChangesIgnoresAnUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	d := t.TempDir()
	putFile(t, filepath.Join(d, "x"), "x", 0o000)
	c := looseCopies{dirs: []string{d}}
	if got := c.changes(); len(got) != 0 {
		t.Errorf("an unreadable file cannot be compared, got %+v", got)
	}
}

func TestCopyLooseRefusesWhatChangedSinceTheListing(t *testing.T) {
	d, dst := t.TempDir(), filepath.Join(t.TempDir(), "x")
	if err := syscall.Mkfifo(filepath.Join(d, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(d, "real"), "r", 0o644)
	_ = os.Symlink("real", filepath.Join(d, "link"))
	putFile(t, filepath.Join(d, "big"), strings.Repeat("x", maxLooseFile+1), 0o644)
	for _, n := range []string{"pipe", "link", "big", "nope"} {
		if _, ok := copyLoose(filepath.Join(d, n), dst); ok { // the FIFO case would hang if it were opened to read
			t.Errorf("%s must not be copied", n)
		}
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("nothing should have been written")
	}
}

func TestReadLimited(t *testing.T) {
	if d, ok := readLimited(strings.NewReader("abc")); !ok || string(d) != "abc" {
		t.Errorf("small read: %q %v", d, ok)
	}
	if _, ok := readLimited(strings.NewReader(strings.Repeat("x", maxLooseFile+1))); ok {
		t.Error("a longer read must be refused")
	}
	if _, ok := readLimited(strings.NewReader(strings.Repeat("x", maxLooseFile))); !ok {
		t.Error("a read of exactly the limit is fine")
	}
}

func TestChangesReportsANewFileInAFolderWithNoLooseFiles(t *testing.T) {
	d := t.TempDir()
	var c looseCopies
	c.stage(t.TempDir(), d, nil)
	if got := c.changes(); len(got) != 0 {
		t.Fatalf("nothing happened yet, got %+v", got)
	}
	putFile(t, filepath.Join(d, "new"), "n", 0o644)
	putFile(t, filepath.Join(d, "huge"), strings.Repeat("x", maxLooseFile+10), 0o644)
	got := map[string]sandbox.Kind{}
	for _, ch := range c.changes() {
		got[filepath.Base(ch.Path)] = ch.Kind
	}
	if len(got) != 2 || got["new"] != sandbox.Created || got["huge"] != sandbox.Created {
		t.Errorf("got %v", got)
	}
}
