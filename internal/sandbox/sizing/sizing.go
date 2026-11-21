// Package sizing looks, read-only, at how much data a command could put in the
// sandbox's scratch layer, and at what the machine can hold. It never follows
// a symlink and gives up at a file-count and time budget, so looking is cheap
// even in a huge folder. The decision itself is sandbox.Place.
package sizing

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

const (
	// ramMax caps the in-memory scratch layer. The layer is a tmpfs, so what it
	// holds is real memory; 2 GiB is room for a large build tree and still
	// leaves an ordinary machine usable.
	ramMax = 2 << 30
	// maxFiles and maxTime are the look's budget (files and folders seen, time).
	maxFiles = 200_000
	maxTime  = time.Second
)

// Sizer measures for the sandbox. The zero value works; Dir is where an
// on-disk scratch layer would go (empty means none is possible).
type Sizer struct {
	Dir      string
	Meminfo  string        // file to read free memory from; "" means /proc/meminfo
	MaxFiles int           // 0 means the default budget
	MaxTime  time.Duration // 0 means the default budget
}

// Size measures the data under paths, and the room for it in memory and on disk.
func (s Sizer) Size(ctx context.Context, paths []string) sandbox.Size {
	mem := s.Meminfo
	if mem == "" {
		mem = "/proc/meminfo"
	}
	b, _ := os.ReadFile(mem)
	size := sandbox.Size{RAMLimit: RAMLimit(string(b))}
	if s.Dir != "" {
		size.DiskFree = freeSpace(s.Dir, syscall.Statfs)
	}
	files, limit := s.MaxFiles, s.MaxTime
	if files == 0 {
		files = maxFiles
	}
	if limit == 0 {
		limit = maxTime
	}
	// Past what either place could hold the answer is "skip", so look no further.
	size.Bytes, size.Cut = Walk(ctx, paths, max(size.RAMLimit, size.DiskFree/2), files, limit)
	return size
}

// RAMLimit is half of the available memory in meminfo (the text of
// /proc/meminfo), at most 2 GiB. When it cannot be read it is 2 GiB.
func RAMLimit(meminfo string) int64 {
	for _, line := range strings.Split(meminfo, "\n") {
		if rest, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				break
			}
			if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
				return min(kb<<10/2, ramMax)
			}
		}
	}
	return ramMax
}

// freeSpace is the free bytes on the disk that holds dir (or its nearest
// existing parent, since it is created on first use); 0 when unknown.
func freeSpace(dir string, statfs func(string, *syscall.Statfs_t) error) int64 {
	for {
		var st syscall.Statfs_t
		if err := statfs(dir, &st); err == nil {
			return int64(st.Bavail) * int64(st.Bsize)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0
		}
		dir = parent
	}
}

// Walk adds up the sizes of the regular files under paths without following
// symlinks. It stops, with cut set, when the sum passes stop, after maxFiles
// entries, after limit, or when ctx ends. A folder inside an earlier path is
// not counted twice. Sizes are apparent sizes, so a sparse file counts whole.
func Walk(ctx context.Context, paths []string, stop int64, maxFiles int, limit time.Duration) (bytes int64, cut bool) {
	deadline := time.Now().Add(limit)
	seen := 0
	var done []string
	for _, root := range paths {
		if covered(done, root) {
			continue
		}
		done = append(done, root)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil // missing or unreadable: nothing to count
			}
			seen++
			if bytes > stop || seen > maxFiles || ctx.Err() != nil || time.Now().After(deadline) {
				cut = true
				return fs.SkipAll
			}
			if d.Type().IsRegular() {
				if fi, err := d.Info(); err == nil {
					bytes += fi.Size()
				}
			}
			return nil
		})
		if cut {
			break
		}
	}
	return bytes, cut
}

// covered says whether p is, or lies inside, one of the folders already counted.
func covered(done []string, p string) bool {
	for _, d := range done {
		if p == d || strings.HasPrefix(p, strings.TrimSuffix(d, "/")+"/") {
			return true
		}
	}
	return false
}
