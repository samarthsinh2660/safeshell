//go:build linux

package overlayfs

import (
	"crypto/sha256"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// looseCopies tracks the loose files copied into the sandbox, so that what the
// command did to them can be reported.
type looseCopies struct {
	dirs   []string             // partly mirrored folders, compared after the run
	before map[string]fileState // state of each copied file when the run began
	used   int64                // bytes copied so far
}

// stage copies the regular loose files of dir that fit the limits into the same
// folder of the new root and returns the loose files left out. The real files
// are only read.
func (c *looseCopies) stage(merged, dir string, entries []fs.DirEntry) (skipped int) {
	var files []looseFile
	for _, e := range entries {
		if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
			continue
		}
		var size int64 = maxLooseFile + 1 // anything that is not a regular file is left out
		if fi, err := e.Info(); err == nil && fi.Mode().IsRegular() {
			size = fi.Size()
		}
		files = append(files, looseFile{Path: filepath.Join(dir, e.Name()), Size: size})
	}
	chosen, left := pickLoose(files, maxLooseFile, maxLooseTotal-c.used)
	skipped = len(left)
	c.dirs = append(c.dirs, dir)
	for _, f := range chosen {
		st, ok := copyLoose(f.Path, filepath.Join(merged, f.Path))
		if !ok {
			skipped++
			continue
		}
		if c.before == nil {
			c.before = map[string]fileState{}
		}
		c.before[f.Path] = st
		c.used += st.Size
	}
	return skipped
}

// readLimited reads r up to maxLooseFile bytes; ok is false when there is more.
func readLimited(r io.Reader) (data []byte, ok bool) {
	data, err := io.ReadAll(io.LimitReader(r, maxLooseFile+1))
	return data, err == nil && len(data) <= maxLooseFile
}

// copyLoose copies src to dst keeping its mode and modification time, and
// returns the state of the copy (the one later runs are compared with). The file
// may have changed since the folder was listed, so it is opened without
// following links or waiting on a pipe, checked again once open, and read only up
// to the limit.
func copyLoose(src, dst string) (fileState, bool) {
	f, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fileState{}, false
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxLooseFile {
		return fileState{}, false
	}
	data, ok := readLimited(f)
	if !ok || os.WriteFile(dst, data, 0o600) != nil {
		return fileState{}, false
	}
	_ = os.Chmod(dst, fi.Mode().Perm())
	_ = os.Chtimes(dst, fi.ModTime(), fi.ModTime())
	return currentState(dst)
}

func stateOf(fi fs.FileInfo, data []byte) fileState {
	return fileState{Size: fi.Size(), Mode: uint32(fi.Mode()), Mtime: fi.ModTime().UnixNano(), Sum: sha256.Sum256(data)}
}

// changes reads the copied folders as the command left them. It runs after the
// pivot, where each folder is at its real path.
func (c *looseCopies) changes() []sandbox.Change {
	after := map[string]fileState{}
	for _, dir := range c.dirs {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.IsDir() || e.Type()&os.ModeSymlink != 0 {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if st, ok := currentState(p); ok {
				after[p] = st
			}
		}
	}
	return diffLoose(c.before, after)
}

func currentState(p string) (fileState, bool) {
	fi, err := os.Lstat(p)
	if err != nil || !fi.Mode().IsRegular() {
		return fileState{}, false
	}
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fileState{}, false
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, maxLooseFile+1)) // a bigger file is still seen, by its size and first bytes
	return stateOf(fi, data), true
}
