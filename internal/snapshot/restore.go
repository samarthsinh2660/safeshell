package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	case File:
		if statErr == nil && cur.Mode().IsRegular() {
			if sum, err := hashFile(e.Path); err == nil && sum == e.SHA {
				if cur.Mode().Perm() != os.FileMode(e.Mode).Perm() {
					return true, os.Chmod(e.Path, os.FileMode(e.Mode))
				}
				return false, nil
			}
		}
		if statErr == nil && cur.IsDir() {
			if err := os.RemoveAll(e.Path); err != nil {
				return false, err
			}
		}
		_ = os.MkdirAll(filepath.Dir(e.Path), 0o755)
		return true, writeBlob(filepath.Join(dir(store, id), "blobs", e.SHA), e)
	}
	return false, fmt.Errorf("unknown entry kind %q", e.Kind)
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

// Verify compares the live files with the manifest and lists every difference.
func Verify(store string, m Manifest) []string {
	var bad []string
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
			bad = append(bad, e.Path+" is missing")
			continue
		}
		switch e.Kind {
		case Dir:
			if !cur.IsDir() {
				bad = append(bad, e.Path+" is not a folder")
			}
		case Symlink:
			if t, err := os.Readlink(e.Path); err != nil || t != e.Link {
				bad = append(bad, e.Path+" points somewhere else")
			}
		case File:
			if !cur.Mode().IsRegular() {
				bad = append(bad, e.Path+" is not a regular file")
			} else if sum, err := hashFile(e.Path); err != nil || sum != e.SHA {
				bad = append(bad, e.Path+" has different contents")
			}
		}
		if e.Kind != Symlink && err == nil &&
			cur.Mode().Perm() != os.FileMode(e.Mode).Perm() {
			bad = append(bad, e.Path+" has different permissions")
		}
	}
	return bad
}
