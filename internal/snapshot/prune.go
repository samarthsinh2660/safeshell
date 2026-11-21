package snapshot

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/report"
)

// ErrNoSpace means the disk that holds the snapshots is too full to take one.
var ErrNoSpace = errors.New("not enough free disk space to snapshot")

// reserveMax is how much free space a snapshot always leaves on the disk. On a
// small disk the reserve is 5% of it instead, so a snapshot is never refused
// only because the whole disk is small.
const reserveMax = 1 << 30

// preflight fails before anything is copied when the targets are over limit
// (0 means no limit) or the disk cannot hold them. space reports free and
// total bytes of the disk that holds path.
func preflight(store string, targets []Target, limit int64, space func(path string) (free, total int64, err error)) error {
	size, over := estimate(targets, limit)
	if over {
		return fmt.Errorf("%w: the files are bigger than the %s limit", ErrTooLarge, report.HumanBytes(limit))
	}
	free, total, err := space(existingParent(store))
	if err != nil {
		return nil // cannot tell; the copy itself reports a full disk
	}
	return checkSpace(size, free, total)
}

// estimate adds up the sizes of the regular files under the targets, and stops
// as soon as the sum passes limit.
func estimate(targets []Target, limit int64) (size int64, over bool) {
	seen := map[string]bool{}
	for _, t := range targets {
		_ = filepath.WalkDir(resolveParents(t.Path), func(p string, de fs.DirEntry, err error) error {
			if err != nil || seen[p] || !de.Type().IsRegular() {
				return nil
			}
			seen[p] = true
			if fi, err := de.Info(); err == nil {
				size += fi.Size()
			}
			if limit > 0 && size > limit {
				over = true
				return fs.SkipAll
			}
			return nil
		})
		if over {
			break
		}
	}
	return size, over
}

// checkSpace is the free-space rule: size plus a reserve must fit in free.
func checkSpace(size, free, total int64) error {
	reserve := min(int64(reserveMax), total/20)
	if free-reserve >= size {
		return nil
	}
	return fmt.Errorf("%w: only %s is free on the disk that holds snapshots", ErrNoSpace, report.HumanBytes(free))
}

// existingParent is the nearest folder at or above p that exists, since the
// store is created by the first snapshot.
func existingParent(p string) string {
	for {
		if _, err := os.Stat(p); err == nil || filepath.Dir(p) == p {
			return p
		}
		p = filepath.Dir(p)
	}
}

func diskSpace(path string) (free, total int64, err error) {
	var s syscall.Statfs_t
	if err = syscall.Statfs(path, &s); err != nil {
		return 0, 0, err
	}
	return int64(s.Bavail) * int64(s.Bsize), int64(s.Blocks) * int64(s.Bsize), nil
}

// Pruned says what Prune did.
type Pruned struct {
	Removed, Left int
	Freed, Size   int64 // bytes removed, bytes still saved
}

// Prune deletes the oldest snapshots until none is older than olderThan and
// the rest total at most maxBytes, never the one named keep. A limit of 0 is
// not applied. It also removes the notes left for terminals whose snapshot no
// longer exists.
func Prune(store string, olderThan time.Duration, maxBytes int64, keep string) (Pruned, error) {
	return prune(OS(), store, olderThan, maxBytes, keep)
}

func prune(h Host, store string, olderThan time.Duration, maxBytes int64, keep string) (Pruned, error) {
	var r Pruned
	ms, err := List(store)
	if err != nil {
		return r, err
	}
	for _, m := range ms {
		r.Size += m.Bytes
	}
	cutoff := time.Now().Add(-olderThan)
	var undeletable error               // the first filesystem snapshot that could not be deleted; the rest are still pruned
	for i := len(ms) - 1; i >= 0; i-- { // oldest first
		m := ms[i]
		if m.ID == keep {
			continue
		}
		old := olderThan > 0 && m.Created.Before(cutoff)
		big := maxBytes > 0 && r.Size > maxBytes
		if !old && !big {
			break // every later one is newer, and the total no longer shrinks
		}
		if m.Native != nil { // the filesystem snapshot goes first: it is not an ordinary folder
			if err := h.Delete(*m.Native); err != nil {
				if undeletable == nil {
					undeletable = err
				}
				continue // keep its folder: it is how safeshell finds the snapshot again
			}
		}
		if err := os.RemoveAll(dir(store, m.ID)); err != nil {
			return r, err
		}
		r.Removed++
		r.Freed += m.Bytes
		r.Size -= m.Bytes
	}
	r.Left = len(ms) - r.Removed
	dropOrphanNotes(store)
	return r, undeletable
}

// dropOrphanNotes removes .unsealed-<pid> notes that name a deleted snapshot.
func dropOrphanNotes(store string) {
	notes, _ := filepath.Glob(filepath.Join(store, ".unsealed-*"))
	for _, n := range notes {
		id, err := os.ReadFile(n)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir(store, strings.TrimSpace(string(id))), "manifest.json")); errors.Is(err, fs.ErrNotExist) {
			_ = os.Remove(n)
		}
	}
}
