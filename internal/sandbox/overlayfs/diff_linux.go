package overlayfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

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
func Diff(upper, lower string, opt Options) (sandbox.FileDiff, error) {
	opt = opt.withDefaults()
	var d sandbox.FileDiff
	err := filepath.WalkDir(upper, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			// The command may have taken the search permission off a folder
			// (chmod -R 600 .); its copy in the scratch area must stay readable.
			// Only the scratch copy changes, never a real file.
			_ = os.Chmod(p, 0o700)
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
			c := sandbox.Change{Path: rel, Kind: sandbox.Deleted, Files: 1}
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
				d.Changes = append(d.Changes, sandbox.Change{Path: rel, Kind: sandbox.Created, IsDir: true, Files: 0})
			case opt.IsOpaque(p):
				c := sandbox.Change{Path: rel, Kind: sandbox.Replaced, IsDir: true}
				d.Truncated = count(lowerPath, opt.MaxExpand, &c) || d.Truncated
				d.Changes = append(d.Changes, c)
			}
			return nil
		}
		k := sandbox.Created
		if inLower {
			k = sandbox.Modified
		}
		d.Changes = append(d.Changes, sandbox.Change{Path: rel, Kind: k, Files: 1, Bytes: fi.Size()})
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return d, err
}

// count adds the files under root to c, stopping after max entries. It reports
// whether it stopped early.
func count(root string, max int, c *sandbox.Change) (truncated bool) {
	seen := 0
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > max {
			truncated = true
			return fs.SkipAll
		}
		if rel, _ := filepath.Rel(root, p); len(c.Notable) < 5 && sandbox.IsNotable(rel) {
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
