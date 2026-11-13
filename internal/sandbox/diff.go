// Package sandbox defines what a simulation reports and reads the OverlayFS
// upper directory to find out what a command changed. Running the command
// itself lives in sandbox/overlayfs.
package sandbox

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// Kind says what happened to a path.
type Kind string

const (
	Created  Kind = "created"
	Modified Kind = "modified"
	Deleted  Kind = "deleted"
	Replaced Kind = "replaced" // a directory emptied and recreated
)

// Change is one top-level difference. For a deleted directory, Files counts
// everything that was inside it.
type Change struct {
	Path  string // relative to the root, no leading slash
	Kind  Kind
	IsDir bool
	Files int   // files affected (1 for a plain file)
	Bytes int64 // bytes in those files
	// Notable lists up to five paths inside a deleted or replaced tree that look
	// important (git history, keys, databases, documents).
	Notable []string
}

// Abs is the change's path with its leading slash.
func (c Change) Abs() string { return "/" + c.Path }

// FileDiff is the observed effect of a command on the filesystem.
type FileDiff struct {
	Changes   []Change
	Truncated bool // a large deleted tree was counted only up to a limit
}

// Totals sums files and bytes per kind.
type Totals struct {
	Created, Modified, Deleted int
	DeletedBytes               int64
}

func (d FileDiff) Totals() Totals {
	var t Totals
	for _, c := range d.Changes {
		switch c.Kind {
		case Created:
			t.Created += c.Files
		case Modified:
			t.Modified += c.Files
		case Deleted, Replaced:
			t.Deleted += c.Files
			t.DeletedBytes += c.Bytes
		}
	}
	return t
}

// Options lets tests replace the two checks that need a real overlay mount.
type Options struct {
	// IsWhiteout reports whether an upper entry marks a deletion.
	IsWhiteout func(path string, fi fs.FileInfo) bool
	// IsOpaque reports whether an upper directory replaces the lower one.
	IsOpaque func(path string) bool
	// MaxExpand bounds how many entries of a deleted tree are counted.
	MaxExpand int
}

func (o Options) withDefaults() Options {
	if o.IsWhiteout == nil {
		o.IsWhiteout = func(_ string, fi fs.FileInfo) bool { return isWhiteoutInfo(fi) }
	}
	if o.IsOpaque == nil {
		o.IsOpaque = isOpaqueDir
	}
	if o.MaxExpand == 0 {
		o.MaxExpand = 500_000
	}
	return o
}

// isWhiteoutInfo: OverlayFS records a deletion as a character device 0:0.
func isWhiteoutInfo(fi fs.FileInfo) bool {
	if fi.Mode()&fs.ModeCharDevice == 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Rdev == 0
}

// isOpaqueDir reads the overlay "opaque" marker. Inside a user namespace the
// attribute is user.overlay.opaque, otherwise trusted.overlay.opaque.
func isOpaqueDir(path string) bool {
	buf := make([]byte, 4)
	for _, name := range []string{"user.overlay.opaque", "trusted.overlay.opaque"} {
		if n, err := syscall.Getxattr(path, name, buf); err == nil && n > 0 && buf[0] == 'y' {
			return true
		}
	}
	return false
}

// Diff compares an OverlayFS upper directory with the lower tree it covered.
// lower is the root the upper paths are relative to, "/" for a real run.
func Diff(upper, lower string, opt Options) (FileDiff, error) {
	opt = opt.withDefaults()
	var d FileDiff
	err := filepath.WalkDir(upper, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(upper, p)
		if rel == "." {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return err
		}
		lowerPath := filepath.Join(lower, rel)
		_, lerr := os.Lstat(lowerPath)
		inLower := lerr == nil

		switch {
		case opt.IsWhiteout(p, fi):
			c := Change{Path: rel, Kind: Deleted, Files: 1}
			if lfi, err := os.Lstat(lowerPath); err == nil {
				c.IsDir = lfi.IsDir()
				c.Files, c.Bytes = 0, 0
				d.Truncated = count(lowerPath, opt.MaxExpand, &c) || d.Truncated
			}
			d.Changes = append(d.Changes, c)
			return nil
		case e.IsDir():
			switch {
			case !inLower:
				d.Changes = append(d.Changes, Change{Path: rel, Kind: Created, IsDir: true, Files: 0})
			case opt.IsOpaque(p):
				c := Change{Path: rel, Kind: Replaced, IsDir: true}
				d.Truncated = count(lowerPath, opt.MaxExpand, &c) || d.Truncated
				d.Changes = append(d.Changes, c)
			}
			return nil
		}
		k := Created
		if inLower {
			k = Modified
		}
		d.Changes = append(d.Changes, Change{Path: rel, Kind: k, Files: 1, Bytes: fi.Size()})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return d, err
}

// count adds the files under root to c, stopping after max entries. It reports
// whether it stopped early.
func count(root string, max int, c *Change) (truncated bool) {
	seen := 0
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > max {
			truncated = true
			return fs.SkipAll
		}
		if rel, _ := filepath.Rel(root, p); len(c.Notable) < 5 && IsNotable(rel) {
			c.Notable = append(c.Notable, filepath.Join(filepath.Base(root), rel))
		}
		if !e.IsDir() {
			c.Files++
			if fi, err := e.Info(); err == nil {
				c.Bytes += fi.Size()
			}
		}
		return nil
	})
	return truncated
}
