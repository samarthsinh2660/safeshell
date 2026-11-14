package sandbox

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Real whiteouts need root to create, so tests mark a deletion with a ".wh"
// suffix and an opaque directory with a ".opaque" file inside it.
var fake = Options{
	IsWhiteout: func(p string, _ fs.FileInfo) bool { return strings.HasSuffix(p, ".wh") },
	IsOpaque: func(p string) bool {
		_, err := os.Stat(filepath.Join(p, ".opaque"))
		return err == nil
	},
}

func byPath(d FileDiff) map[string]Change {
	m := map[string]Change{}
	for _, c := range d.Changes {
		m[c.Path] = c
	}
	return m
}

func TestDiffCreatedAndModified(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	write(t, lower, "keep.txt", "unchanged")
	write(t, lower, "edit.txt", "old")

	write(t, upper, "edit.txt", "new content") // copied up and changed
	write(t, upper, "fresh.txt", "hello")      // brand new
	write(t, upper, "newdir/x", "x")           // new directory with a file

	d, err := Diff(upper, lower, fake)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(d)
	if c := m["edit.txt"]; c.Kind != Modified || c.Bytes != 11 || c.Files != 1 {
		t.Errorf("edit.txt: %+v", c)
	}
	if c := m["fresh.txt"]; c.Kind != Created || c.Files != 1 {
		t.Errorf("fresh.txt: %+v", c)
	}
	if c := m["newdir"]; c.Kind != Created || !c.IsDir {
		t.Errorf("newdir: %+v", c)
	}
	if c := m["newdir/x"]; c.Kind != Created {
		t.Errorf("newdir/x: %+v", c)
	}
	if _, ok := m["keep.txt"]; ok {
		t.Error("an untouched file must not appear")
	}
	if len(d.Changes) != 4 {
		var names []string
		for _, c := range d.Changes {
			names = append(names, c.Path)
		}
		sort.Strings(names)
		t.Errorf("changes = %v, want 4", names)
	}
	if tot := d.Totals(); tot.Created != 2 || tot.Modified != 1 || tot.Deleted != 0 {
		t.Errorf("totals = %+v", tot)
	}
}

func TestDeletedFileInsideModifiedDirectory(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	write(t, lower, "d/gone.txt", "12345")
	write(t, lower, "d/stay.txt", "z")
	write(t, upper, "d/gone.txt", "") // a real whiteout has the deleted file's own name
	opt := Options{IsWhiteout: func(p string, _ fs.FileInfo) bool { return filepath.Base(p) == "gone.txt" }}
	d, _ := Diff(upper, lower, opt)
	m := byPath(d)
	if c := m["d/gone.txt"]; c.Kind != Deleted || c.Files != 1 || c.Bytes != 5 || c.IsDir {
		t.Errorf("d/gone.txt = %+v", c)
	}
	if _, ok := m["d"]; ok {
		t.Error("a directory that only contains a change is not itself a change")
	}
}

func TestDeletedDirectoryCountsEverythingInside(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	write(t, lower, "proj/a.go", "aaaa")
	write(t, lower, "proj/sub/b.go", "bb")
	write(t, lower, "proj/sub/c.go", "c")
	// a real whiteout has the same name as what it deletes
	write(t, upper, "proj", "")
	opt := Options{IsWhiteout: func(p string, _ fs.FileInfo) bool { return filepath.Base(p) == "proj" }}

	d, err := Diff(upper, lower, opt)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Changes) != 1 {
		t.Fatalf("changes = %+v", d.Changes)
	}
	c := d.Changes[0]
	if c.Kind != Deleted || !c.IsDir || c.Files != 3 || c.Bytes != 7 {
		t.Errorf("deleted dir = %+v, want 3 files / 7 bytes", c)
	}
	if tot := d.Totals(); tot.Deleted != 3 || tot.DeletedBytes != 7 || tot.Created != 0 {
		t.Errorf("totals = %+v", tot)
	}
}

func TestOpaqueDirectoryIsReplaced(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	write(t, lower, "cache/old1", "1")
	write(t, lower, "cache/old2", "22")
	write(t, upper, "cache/.opaque", "")
	write(t, upper, "cache/new", "n")
	d, err := Diff(upper, lower, fake)
	if err != nil {
		t.Fatal(err)
	}
	m := byPath(d)
	if c := m["cache"]; c.Kind != Replaced || c.Files != 2 || c.Bytes != 3 {
		t.Errorf("cache = %+v", c)
	}
	if c := m["cache/new"]; c.Kind != Created {
		t.Errorf("cache/new = %+v", c)
	}
}

func TestExpansionIsBounded(t *testing.T) {
	lower, upper := t.TempDir(), t.TempDir()
	for i := 0; i < 50; i++ {
		write(t, lower, "big/f"+string(rune('a'+i%26))+string(rune('a'+i/26)), "x")
	}
	write(t, upper, "big", "")
	opt := Options{IsWhiteout: func(p string, _ fs.FileInfo) bool { return filepath.Base(p) == "big" }, MaxExpand: 10}
	d, _ := Diff(upper, lower, opt)
	if !d.Truncated || d.Changes[0].Files >= 50 {
		t.Errorf("expected a truncated count, got %+v truncated=%v", d.Changes, d.Truncated)
	}
}

func TestEmptyUpperAndMissingUpper(t *testing.T) {
	d, err := Diff(t.TempDir(), t.TempDir(), fake)
	if err != nil || len(d.Changes) != 0 {
		t.Errorf("empty upper: %+v %v", d, err)
	}
	d, err = Diff(filepath.Join(t.TempDir(), "nope"), t.TempDir(), fake)
	if err != nil || len(d.Changes) != 0 {
		t.Errorf("missing upper should be an empty diff: %+v %v", d, err)
	}
}

type fakeInfo struct {
	mode fs.FileMode
	rdev uint64
}

func (f fakeInfo) Name() string       { return "x" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return &syscall.Stat_t{Rdev: f.rdev} }

func TestWhiteoutDetection(t *testing.T) {
	if !isWhiteoutInfo(fakeInfo{mode: fs.ModeDevice | fs.ModeCharDevice, rdev: 0}) {
		t.Error("char device 0:0 is a whiteout")
	}
	if isWhiteoutInfo(fakeInfo{mode: fs.ModeDevice | fs.ModeCharDevice, rdev: 0x0501}) {
		t.Error("a real char device is not a whiteout")
	}
	if isWhiteoutInfo(fakeInfo{mode: 0o644}) {
		t.Error("a regular file is not a whiteout")
	}
}

func TestTotals(t *testing.T) {
	d := FileDiff{Changes: []Change{
		{Kind: Created, Files: 2}, {Kind: Modified, Files: 1}, {Kind: Deleted, Files: 10, Bytes: 100}, {Kind: Replaced, Files: 4, Bytes: 8},
	}}
	if got := d.Totals(); got != (Totals{Created: 2, Modified: 1, Deleted: 14, DeletedBytes: 108}) {
		t.Errorf("totals = %+v", got)
	}
}

func TestChangeAbsAddsTheLeadingSlash(t *testing.T) {
	if got := (Change{Path: "home/me/a.txt"}).Abs(); got != "/home/me/a.txt" {
		t.Error(got)
	}
}

func TestSpecDefaults(t *testing.T) {
	d := Spec{Line: "ls"}.Defaults()
	if d.Timeout != 30*time.Second || d.MaxOutput != 64<<10 || d.Line != "ls" {
		t.Errorf("%+v", d)
	}
	set := Spec{Timeout: time.Second, MaxOutput: 5}.Defaults()
	if set.Timeout != time.Second || set.MaxOutput != 5 {
		t.Errorf("values that were set must stay: %+v", set)
	}
}

func TestOptionsDefaults(t *testing.T) {
	o := Options{}.withDefaults()
	if o.MaxExpand != 500_000 || o.IsWhiteout == nil || o.IsOpaque == nil {
		t.Fatalf("%+v", o)
	}
	dir := t.TempDir()
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if o.IsWhiteout(dir, fi) || o.IsOpaque(dir) {
		t.Error("an ordinary directory is neither a whiteout nor opaque")
	}
	kept := Options{MaxExpand: 3}.withDefaults()
	if kept.MaxExpand != 3 {
		t.Error("a set MaxExpand must stay")
	}
}
