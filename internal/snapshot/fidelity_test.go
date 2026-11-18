package snapshot

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func mustMkfifo(t *testing.T, p string) {
	t.Helper()
	if err := syscall.Mkfifo(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
}

func hasProblem(ps []string, want string) bool {
	for _, p := range ps {
		if strings.Contains(p, want) {
			return true
		}
	}
	return false
}

func isPipe(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeNamedPipe != 0
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}

// #95: a pipe in a saved folder used to make the whole snapshot fail.
func TestFifoInFolderIsSavedAndRecreated(t *testing.T) {
	root, store := tree(t)
	proj := filepath.Join(root, "proj")
	fifo := filepath.Join(proj, "sub/pipe")
	mustMkfifo(t, fifo)
	m, err := Create(store, "rm -rf proj", []Target{{Path: proj}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(proj)
	r, err := Restore(store, m.ID)
	if err != nil || len(r.Problems) != 0 {
		t.Fatalf("%v %v", err, r.Problems)
	}
	if read(t, filepath.Join(proj, "a.txt")) != "alpha" || !isPipe(fifo) {
		t.Error("files and the pipe should be back")
	}
	if fi := mustStat(t, fifo); fi.Mode().Perm() != 0o640 {
		t.Errorf("pipe mode %v", fi.Mode())
	}
	// A pipe that is still there is left alone; one replaced by a file is recreated.
	if r, _ := Restore(store, m.ID); r.Restored != 0 || len(r.Problems) != 0 {
		t.Errorf("second restore changed things: %+v", r)
	}
	os.Remove(fifo)
	write(t, fifo, "not a pipe", 0o644)
	if r, _ := Restore(store, m.ID); len(r.Problems) != 0 || !isPipe(fifo) {
		t.Errorf("pipe not recreated: %+v", r)
	}
	// Verify says so when it is not a pipe.
	os.Remove(fifo)
	write(t, fifo, "x", 0o640)
	if !hasProblem(Verify(store, m), "is not a pipe") {
		t.Error("verify should notice")
	}
}

func TestFifoRestoreFailures(t *testing.T) {
	notRoot(t)
	d := t.TempDir()
	e := Entry{Path: filepath.Join(d, "p"), Kind: Fifo, Mode: 0o600}
	readOnly(t, d)
	if _, err := restoreEntry(t.TempDir(), "id", e); err == nil {
		t.Error("cannot create a pipe in a read-only folder")
	}
	os.Chmod(d, 0o755)
	write(t, filepath.Join(d, "p/x"), "x", 0o644) // a folder where the pipe should be
	readOnly(t, d)
	if _, err := restoreEntry(t.TempDir(), "id", e); err == nil {
		t.Error("cannot replace a folder in a read-only folder")
	}
}

func TestSocketAndDeviceAreRecordedButNotRecreated(t *testing.T) {
	dir, err := os.MkdirTemp("", "s") // unix socket paths are short
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Skip("no unix sockets:", err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	write(t, filepath.Join(dir, "a.txt"), "alpha", 0o644)
	store := t.TempDir()
	m, err := Create(store, "rm -rf d", []Target{{Path: dir}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := Restore(store, m.ID); len(r.Problems) != 0 {
		t.Errorf("an untouched socket is fine: %v", r.Problems)
	}
	os.Remove(sock)
	r, _ := Restore(store, m.ID)
	if !hasProblem(r.Problems, "sockets and devices cannot be recreated") {
		t.Errorf("a deleted socket must be reported: %v", r.Problems)
	}
	if read(t, filepath.Join(dir, "a.txt")) != "alpha" {
		t.Error("other files still restored")
	}
}

// #95: an unreadable folder or file used to make the whole snapshot fail.
func TestUnreadablePathsAreLeftOutNotFatal(t *testing.T) {
	notRoot(t)
	root, store := tree(t)
	proj := filepath.Join(root, "proj")
	locked := filepath.Join(proj, "locked")
	private := filepath.Join(proj, "private.txt")
	write(t, filepath.Join(locked, "secret"), "s", 0o644)
	write(t, private, "p", 0o000)
	os.Chmod(locked, 0o000)
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	m, err := Create(store, "rm -rf proj", []Target{{Path: proj}, {Path: filepath.Join(locked, "secret")}}, 0)
	if err != nil {
		t.Fatalf("snapshot must still succeed: %v", err)
	}
	got := map[string]string{}
	for _, l := range m.LeftOut {
		got[l.Path] = l.Reason
	}
	if got[locked] != "permission denied" || got[private] != "permission denied" || got[filepath.Join(locked, "secret")] != "permission denied" {
		t.Errorf("left out: %+v", m.LeftOut)
	}
	os.Chmod(locked, 0o755)
	os.Remove(private)
	os.Chmod(locked, 0o000)
	os.RemoveAll(filepath.Join(proj, "sub"))
	os.Remove(filepath.Join(proj, "a.txt"))
	r, _ := Restore(store, m.ID)
	if read(t, filepath.Join(proj, "a.txt")) != "alpha" || read(t, filepath.Join(proj, "sub/deep/c.txt")) != "gamma" {
		t.Error("the readable files must be restored")
	}
	if hasProblem(r.Problems, "is new") {
		t.Errorf("a left-out path is not reported as new: %v", r.Problems)
	}
	if loaded, _ := Load(store, m.ID); len(loaded.LeftOut) != 3 {
		t.Errorf("left out is kept in the manifest: %+v", loaded.LeftOut)
	}
}

// #96: undo broke the link and left the other name with the changed content.
func TestHardLinkedFileIsRestoredInPlace(t *testing.T) {
	root, store := tree(t)
	f, outside := filepath.Join(root, "proj/f"), filepath.Join(root, "outside")
	write(t, f, "original", 0o644)
	if err := os.Link(f, outside); err != nil {
		t.Fatal(err)
	}
	m, _ := Create(store, "truncate -s 0 proj/f", []Target{{Path: f}}, 0)
	if err := os.Truncate(f, 0); err != nil {
		t.Fatal(err)
	}
	r, _ := Restore(store, m.ID)
	if read(t, f) != "original" || read(t, outside) != "original" || len(r.Problems) != 0 {
		t.Errorf("f %q outside %q problems %v", read(t, f), read(t, outside), r.Problems)
	}
	if !os.SameFile(mustStat(t, f), mustStat(t, outside)) {
		t.Error("the link should be kept")
	}
}

func TestHardLinkThatCannotBeRestoredIsReported(t *testing.T) {
	root, store := tree(t)
	f := filepath.Join(root, "proj/f")
	write(t, f, "original", 0o644)
	os.Link(f, filepath.Join(root, "o1"))
	m, _ := Create(store, "rm proj/f", []Target{{Path: f}}, 0)
	os.Remove(f) // the file is gone, so it comes back as a new file
	r, _ := Restore(store, m.ID)
	if !hasProblem(r.Problems, "1 other hard link to this file was not restored") {
		t.Errorf("problems %v", r.Problems)
	}
	os.Link(f, filepath.Join(root, "o2"))
	os.Link(f, filepath.Join(root, "o3"))
	m, _ = Create(store, "rm proj/f", []Target{{Path: f}}, 0)
	os.Remove(f)
	r, _ = Restore(store, m.ID)
	if !hasProblem(r.Problems, "2 other hard links to this file were not restored") {
		t.Errorf("problems %v", r.Problems)
	}
}

func TestRewriteInPlaceFailures(t *testing.T) {
	d := t.TempDir()
	f := filepath.Join(d, "f")
	blob := filepath.Join(d, "blob")
	write(t, f, "x", 0o644)
	write(t, blob, "y", 0o644)
	if err := rewriteInPlace(filepath.Join(d, "none"), f); err == nil {
		t.Error("missing blob")
	}
	if err := rewriteInPlace(blob, filepath.Join(d, "none")); err == nil {
		t.Error("missing target")
	}
	if _, err := os.Stat("/dev/full"); err == nil {
		if err := rewriteInPlace(blob, "/dev/full"); err == nil {
			t.Error("a full disk is an error")
		}
	}
	// restoreEntry reports it too.
	store := t.TempDir()
	os.MkdirAll(filepath.Join(store, "id", "blobs"), 0o700)
	e := Entry{Path: f, Kind: File, SHA: "feed", Links: 2, Inode: inode(mustStat(t, f))}
	if _, err := restoreEntry(store, "id", e); err == nil {
		t.Error("a blob that is missing from the snapshot")
	}
}

// #97: touch left the new time in place and undo said nothing.
func TestModificationTimeIsRestoredEvenWhenContentMatches(t *testing.T) {
	root, store := tree(t)
	a := filepath.Join(root, "proj/a.txt")
	m, _ := Create(store, "touch a", []Target{{Path: a}}, 0)
	want := mustStat(t, a).ModTime()
	later := want.Add(time.Hour)
	os.Chtimes(a, later, later)
	if !hasProblem(Verify(store, m), "different modification time") {
		t.Error("verify must notice the time")
	}
	r, _ := Restore(store, m.ID)
	if got := mustStat(t, a).ModTime(); !got.Equal(want) || r.Restored != 1 || len(r.Problems) != 0 {
		t.Errorf("mtime %v want %v, %+v", got, want, r)
	}
}

func TestVerifyNoticesADifferentOwner(t *testing.T) {
	root, store := tree(t)
	a := filepath.Join(root, "proj/a.txt")
	m, _ := Create(store, "x", []Target{{Path: a}}, 0)
	m.Entries[0].UID++ // as if the file had belonged to someone else
	if !hasProblem(Verify(store, m), "different owner") {
		t.Error("owner")
	}
}

func TestFixMetaErrors(t *testing.T) {
	if _, err := fixMeta(Entry{Path: filepath.Join(t.TempDir(), "none")}); err == nil {
		t.Error("missing path")
	}
	notRoot(t)
	if _, err := fixMeta(Entry{Path: "/dev/null", Mode: 0o600}); err == nil {
		t.Error("a mode that cannot be set is an error")
	}
}

// #97: a file written into a saved folder after the command is reported, not deleted.
func TestNewFileInSavedFolderIsReported(t *testing.T) {
	root, store := tree(t)
	proj := filepath.Join(root, "proj")
	m, _ := Create(store, "rm -rf proj", []Target{{Path: proj}}, 0)
	extra := filepath.Join(proj, "sub/new.txt")
	write(t, extra, "new", 0o644)
	r, _ := Restore(store, m.ID)
	if !hasProblem(r.Problems, extra+" is new") {
		t.Errorf("problems %v", r.Problems)
	}
	if read(t, extra) != "new" {
		t.Error("the new file must be left alone")
	}
}

// Snapshots from before this change have no inode, link count or left-out list.
func TestOldManifestStillRestores(t *testing.T) {
	root, store := tree(t)
	a := filepath.Join(root, "proj/a.txt")
	m, _ := Create(store, "x", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	for i := range m.Entries {
		m.Entries[i].Inode, m.Entries[i].Links = 0, 0
	}
	m.LeftOut = nil
	if err := writeManifest(store, m); err != nil {
		t.Fatal(err)
	}
	write(t, a, "changed", 0o644)
	if r, err := Restore(store, m.ID); err != nil || len(r.Problems) != 0 || read(t, a) != "alpha" {
		t.Errorf("%v %+v", err, r)
	}
}
