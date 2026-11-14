// Package snapshot saves the files a command is about to change and restores
// them afterwards, then checks that the restore is exact. It copies only the
// paths the sandbox said would change, so it needs no special filesystem.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"time"
)

// Kinds of entry in a manifest.
const (
	File    = "file"
	Dir     = "dir"
	Symlink = "symlink"
	Absent  = "absent" // did not exist; undo removes whatever is there now
)

// ErrTooLarge means the files to protect exceed the size limit.
var ErrTooLarge = errors.New("too much data to snapshot")

// ErrBadID means a snapshot id is not in the format Create produces.
var ErrBadID = errors.New("not a snapshot id")

var idFormat = regexp.MustCompile(`^\d{8}T\d{6}\.\d{9}Z$`)

// Target is a path the command is expected to change.
type Target struct {
	Path   string
	Create bool // the command will create it: undo should remove it
}

// Entry is one saved path.
type Entry struct {
	Path  string `json:"path"`
	Kind  string `json:"kind"`
	Mode  uint32 `json:"mode,omitempty"`
	UID   int    `json:"uid,omitempty"`
	GID   int    `json:"gid,omitempty"`
	Size  int64  `json:"size,omitempty"`
	SHA   string `json:"sha256,omitempty"`
	Link  string `json:"link,omitempty"`
	MTime int64  `json:"mtime,omitempty"` // unix nanoseconds
}

// Manifest describes a snapshot.
type Manifest struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Line    string    `json:"line"`
	Bytes   int64     `json:"bytes"`
	Entries []Entry   `json:"entries"`
}

func dir(store, id string) string { return filepath.Join(store, id) }

// Create saves targets under store and returns the manifest. limit is the most
// file data to copy; 0 means no limit.
func Create(store, line string, targets []Target, limit int64) (m Manifest, err error) {
	id := time.Now().UTC().Format("20060102T150405.000000000Z")
	d := dir(store, id)
	if err = os.MkdirAll(filepath.Join(d, "blobs"), 0o700); err != nil {
		return m, err
	}
	_ = os.Chmod(store, 0o700) // MkdirAll leaves an existing folder as it was; a folder that is not ours stays
	defer func() {
		if err != nil {
			_ = os.RemoveAll(d)
		}
	}()
	m = Manifest{ID: id, Created: time.Now().UTC(), Line: line}
	seen := map[string]bool{}
	add := func(e Entry) {
		if !seen[e.Path] {
			seen[e.Path] = true
			m.Entries = append(m.Entries, e)
		}
	}
	for _, t := range targets {
		// Paths are saved with their parent folders resolved, so a link found among them at
		// undo time can only mean something was swapped in since (see noSymlinkParents).
		path := resolveParents(t.Path)
		// The path may have appeared since the sandbox looked: save it then, so undo never deletes it.
		if _, err := os.Lstat(path); t.Create && errors.Is(err, fs.ErrNotExist) {
			add(Entry{Path: path, Kind: Absent})
			continue
		}
		werr := filepath.WalkDir(path, func(p string, de fs.DirEntry, werr error) error {
			if werr != nil {
				if errors.Is(werr, fs.ErrNotExist) {
					return nil
				}
				return werr
			}
			e, copied, err := capture(d, p, limit-m.Bytes, limit > 0)
			if err != nil {
				return err
			}
			m.Bytes += copied
			add(e)
			return nil
		})
		if werr != nil {
			return m, werr
		}
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Path < m.Entries[j].Path })
	b, _ := json.MarshalIndent(m, "", "  ")
	return m, os.WriteFile(filepath.Join(d, "manifest.json"), b, 0o600)
}

// resolveParents resolves the links in p's parent folders, keeping p's own name (a link
// there is saved as a link). Parents that don't exist yet are kept as written.
func resolveParents(p string) string {
	dir, rest := filepath.Dir(p), []string{filepath.Base(p)}
	for {
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(append([]string{r}, rest...)...)
		}
		rest = append([]string{filepath.Base(dir)}, rest...)
		dir = filepath.Dir(dir)
	}
}

// capture records p, copying file data into the snapshot's blob store.
func capture(snap, p string, room int64, limited bool) (Entry, int64, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return Entry{}, 0, err
	}
	e := Entry{Path: p, Mode: uint32(fi.Mode().Perm() | fi.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky)), MTime: fi.ModTime().UnixNano()}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		e.UID, e.GID = int(st.Uid), int(st.Gid)
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		e.Kind = Symlink
		e.Link, err = os.Readlink(p)
		return e, 0, err
	case fi.IsDir():
		e.Kind = Dir
		return e, 0, nil
	case fi.Mode().IsRegular():
		if limited && fi.Size() > room {
			return e, 0, fmt.Errorf("%w (limit reached at %s)", ErrTooLarge, p)
		}
		e.Kind, e.Size = File, fi.Size()
		e.SHA, err = copyBlob(snap, p)
		return e, fi.Size(), err
	}
	return e, 0, fmt.Errorf("%s is a special file (device, socket or pipe) and cannot be snapshotted", p)
}

func copyBlob(snap, src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Join(snap, "blobs"), "tmp-")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), in); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	sum := hex.EncodeToString(h.Sum(nil))
	return sum, os.Rename(tmp.Name(), filepath.Join(snap, "blobs", sum))
}

// Load reads a snapshot's manifest.
func Load(store, id string) (Manifest, error) {
	var m Manifest
	if !idFormat.MatchString(id) {
		return m, fmt.Errorf("%w: %q", ErrBadID, id)
	}
	b, err := os.ReadFile(filepath.Join(dir(store, id), "manifest.json"))
	if err != nil {
		return m, err
	}
	return m, json.Unmarshal(b, &m)
}

// List returns all snapshots, newest first.
func List(store string) ([]Manifest, error) {
	des, err := os.ReadDir(store)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ms []Manifest
	for _, de := range des {
		if m, err := Load(store, de.Name()); err == nil {
			ms = append(ms, m)
		}
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID > ms[j].ID })
	return ms, nil
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
