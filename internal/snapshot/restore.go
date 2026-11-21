package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Report is the outcome of a restore.
type Report struct {
	Restored int      // paths put back
	Removed  int      // paths the command created that were removed
	Problems []string // anything that does not match the snapshot afterwards; empty means verified
}

// Restore puts every saved path back and then verifies each one against the
// manifest, so "undone" means checked, not hoped.
func Restore(store, id string) (Report, error) {
	var r Report
	m, err := Load(store, id)
	if err != nil {
		return r, err
	}
	// Remove created paths first, deepest first.
	var absent []string
	for _, e := range m.Entries {
		if e.Kind == Absent {
			absent = append(absent, e.Path)
		}
	}
	sort.Slice(absent, func(i, j int) bool { return len(absent[i]) > len(absent[j]) })
	for _, p := range absent {
		if err := noSymlinkParents(p); err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("could not remove %s: %v", p, err))
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			if err := os.RemoveAll(p); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("could not remove %s: %v", p, err))
				continue
			}
			r.Removed++
		}
	}
	for _, e := range m.Entries { // sorted by path, so parents come before children
		if e.Kind == Absent {
			continue
		}
		changed, err := restoreEntry(store, id, e)
		if err != nil {
			r.Problems = append(r.Problems, fmt.Sprintf("could not restore %s: %v", e.Path, err))
		}
		if changed {
			r.Restored++
		}
	}
	// Directory modes and times are set last, deepest first: a read-only folder
	// must stay writable until its contents are back, and writing children
	// changes its mtime.
	for i := len(m.Entries) - 1; i >= 0; i-- {
		if e := m.Entries[i]; e.Kind == Dir && noSymlinkParents(e.Path) == nil {
			if err := os.Chmod(e.Path, os.FileMode(e.Mode)); err != nil {
				r.Problems = append(r.Problems, fmt.Sprintf("could not set permissions on %s: %v", e.Path, err))
			}
			_ = os.Chtimes(e.Path, time.Time{}, time.Unix(0, e.MTime))
		}
	}
	r.Problems = append(r.Problems, Verify(store, m)...)
	return r, nil
}

// noSymlinkParents refuses a path with a symlink among its parent folders, so a
// restore can never be steered to files outside the snapshot.
func noSymlinkParents(p string) error {
	cur := string(filepath.Separator)
	for _, part := range strings.Split(filepath.Dir(p), string(filepath.Separator)) {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return nil // the folder is gone; nothing to follow
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link, so SafeShell will not follow it; check it and restore by hand", cur)
		}
	}
	return nil
}

func restoreEntry(store, id string, e Entry) (changed bool, err error) {
	if err := noSymlinkParents(e.Path); err != nil {
		return false, err
	}
	cur, statErr := os.Lstat(e.Path)
	switch e.Kind {
	case Dir:
		if statErr == nil && cur.IsDir() {
			return false, os.Chmod(e.Path, 0o700) // writable while its contents are put back; the real mode is set at the end
		}
		if statErr == nil {
			if err := os.RemoveAll(e.Path); err != nil {
				return false, err
			}
		}
		if err := os.MkdirAll(e.Path, 0o700); err != nil {
			return false, err
		}
		_ = os.Lchown(e.Path, e.UID, e.GID)
		return true, nil
	case Symlink:
		if statErr == nil {
			if t, err := os.Readlink(e.Path); err == nil && t == e.Link {
				return false, nil
			}
			if err := os.RemoveAll(e.Path); err != nil {
				return false, err
			}
		}
		_ = os.MkdirAll(filepath.Dir(e.Path), 0o755)
		if err := os.Symlink(e.Link, e.Path); err != nil {
			return false, err
		}
		_ = os.Lchown(e.Path, e.UID, e.GID)
		return true, nil
	case Fifo:
		if statErr == nil && cur.Mode()&os.ModeNamedPipe != 0 {
			return fixMeta(e)
		}
		if statErr == nil {
			if err := os.RemoveAll(e.Path); err != nil {
				return false, err
			}
		}
		_ = os.MkdirAll(filepath.Dir(e.Path), 0o755)
		if err := syscall.Mkfifo(e.Path, 0o600); err != nil {
			return false, err
		}
		_, err := fixMeta(e)
		return true, err
	case Special:
		return false, nil // cannot be recreated; Verify says so when it is gone
	case File:
		if statErr == nil && cur.Mode().IsRegular() {
			if sum, err := hashFile(e.Path); err == nil && sum == e.SHA {
				return fixMeta(e)
			}
			if e.Links > 1 && inode(cur) == e.Inode {
				// Other names share this file: write the content back into it, so they see it too.
				if err := rewriteInPlace(blobPath(store, id, e), e.Path); err != nil {
					return false, err
				}
				_, err := fixMeta(e)
				return true, err
			}
		}
		if statErr == nil && cur.IsDir() {
			if err := os.RemoveAll(e.Path); err != nil {
				return false, err
			}
		}
		_ = os.MkdirAll(filepath.Dir(e.Path), 0o755)
		return true, writeBlob(blobPath(store, id, e), e)
	}
	return false, fmt.Errorf("unknown entry kind %q", e.Kind)
}

// blobPath is where the saved content of file e is read from.
func blobPath(store, id string, e Entry) string {
	if e.Src != "" {
		return e.Src
	}
	return filepath.Join(dir(store, id), "blobs", e.SHA)
}

func inode(fi os.FileInfo) uint64 { return fi.Sys().(*syscall.Stat_t).Ino }

// rewriteInPlace replaces the contents of the file at p without making a new file.
func rewriteInPlace(blob, p string) error {
	in, err := os.Open(blob)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(p, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// fixMeta sets the owner, permissions and modification time of e.Path to the saved ones,
// and says whether anything was different. A change of owner that is not allowed (not root)
// is left for Verify to report.
func fixMeta(e Entry) (changed bool, err error) {
	cur, err := os.Lstat(e.Path)
	if err != nil {
		return false, err
	}
	st := cur.Sys().(*syscall.Stat_t)
	if int(st.Uid) == e.UID && int(st.Gid) == e.GID && cur.Mode().Perm() == os.FileMode(e.Mode).Perm() && cur.ModTime().UnixNano() == e.MTime {
		return false, nil
	}
	_ = os.Chown(e.Path, e.UID, e.GID) // clears setuid bits, so it comes before the chmod
	if err := os.Chmod(e.Path, os.FileMode(e.Mode)); err != nil {
		return true, err
	}
	mt := time.Unix(0, e.MTime)
	_ = os.Chtimes(e.Path, mt, mt)
	return true, nil
}

func writeBlob(blob string, e Entry) error {
	in, err := os.Open(blob)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(e.Path), ".ss-restore-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, in); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chown(tmp.Name(), e.UID, e.GID)
	if err := os.Chmod(tmp.Name(), os.FileMode(e.Mode)); err != nil {
		return err
	}
	mt := time.Unix(0, e.MTime)
	_ = os.Chtimes(tmp.Name(), mt, mt)
	return os.Rename(tmp.Name(), e.Path)
}

// Verify compares the live files with the manifest and lists every difference: contents,
// permissions, owner, modification time, hard links that were not put back, and entries in a
// saved folder that the snapshot does not know about.
func Verify(store string, m Manifest) []string {
	var bad []string
	known := map[string]bool{}
	for _, e := range m.Entries {
		known[e.Path] = true
	}
	for _, l := range m.LeftOut {
		known[l.Path] = true
	}
	for _, e := range m.Entries {
		cur, err := os.Lstat(e.Path)
		switch e.Kind {
		case Absent:
			if err == nil {
				bad = append(bad, e.Path+" still exists but did not before")
			}
			continue
		}
		if err != nil {
			if e.Kind == Special {
				bad = append(bad, e.Path+" is missing, and sockets and devices cannot be recreated")
			} else {
				bad = append(bad, e.Path+" is missing")
			}
			continue
		}
		switch e.Kind {
		case Dir:
			if !cur.IsDir() {
				bad = append(bad, e.Path+" is not a folder")
			} else {
				bad = append(bad, unknownEntries(e.Path, known)...)
			}
		case Symlink:
			if t, err := os.Readlink(e.Path); err != nil || t != e.Link {
				bad = append(bad, e.Path+" points somewhere else")
			}
		case Fifo:
			if cur.Mode()&os.ModeNamedPipe == 0 {
				bad = append(bad, e.Path+" is not a pipe")
			}
		case File:
			if !cur.Mode().IsRegular() {
				bad = append(bad, e.Path+" is not a regular file")
			} else if sum, err := hashFile(e.Path); err != nil || sum != e.SHA {
				bad = append(bad, e.Path+" has different contents")
			}
			if e.Links > 1 && cur.Mode().IsRegular() && inode(cur) != e.Inode {
				bad = append(bad, e.Path+": "+otherLinks(e.Links-1)+" to this file "+wasWere(e.Links-1)+" not restored")
			}
		}
		if e.Kind == Symlink || e.Kind == Special {
			continue
		}
		if cur.Mode().Perm() != os.FileMode(e.Mode).Perm() {
			bad = append(bad, e.Path+" has different permissions")
		}
		if st := cur.Sys().(*syscall.Stat_t); int(st.Uid) != e.UID || int(st.Gid) != e.GID {
			bad = append(bad, e.Path+" has a different owner")
		}
		if cur.ModTime().UnixNano() != e.MTime {
			bad = append(bad, e.Path+" has a different modification time")
		}
	}
	return bad
}

func otherLinks(n uint64) string {
	if n == 1 {
		return "1 other hard link"
	}
	return fmt.Sprintf("%d other hard links", n)
}

func wasWere(n uint64) string {
	if n == 1 {
		return "was"
	}
	return "were"
}

// unknownEntries lists what is in folder p that the snapshot does not know: files added
// after the command. Restore leaves them alone; the person is told. A folder that cannot
// be read is skipped (it was recorded as left out).
func unknownEntries(p string, known map[string]bool) (bad []string) {
	des, _ := os.ReadDir(p)
	for _, de := range des {
		if c := filepath.Join(p, de.Name()); !known[c] {
			bad = append(bad, c+" is new: it was added after the snapshot, and undo leaves it alone")
		}
	}
	return bad
}
