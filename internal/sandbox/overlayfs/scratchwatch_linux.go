//go:build linux

package overlayfs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// watchEvery is how often the parent checks how much the command has written
// to an on-disk scratch area.
const watchEvery = 200 * time.Millisecond

// reserveMax matches the free space a snapshot leaves on a disk (see
// internal/snapshot/prune.go): 1 GiB, or 5% of a small disk.
const reserveMax = 1 << 30

// scratchLimit is how much the scratch area on disk may use: half of the free
// space measured before the run, and never so much that less than the reserve
// is left.
func scratchLimit(free, total int64) int64 {
	return max(0, min(free/2, free-min(int64(reserveMax), total/20)))
}

// overLimit says the run has written more than the limit, judged from how far
// free space has fallen since it started.
func overLimit(startFree, free, limit int64) bool {
	return startFree-free > limit
}

// freeSpace reports the free and total bytes of the filesystem that holds dir.
func freeSpace(dir string) (free, total int64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), int64(st.Blocks) * int64(st.Bsize), nil
}

// watchScratch checks free space on each tick and calls stop, once, as soon as
// the run is over limit. It returns true if it stopped the run, and returns
// when ctx ends. Free space is used rather than a walk of the upper folder: one
// statfs call costs the same however many files there are, counts only blocks
// really written (sparse files are free), and also notices another process
// filling the disk, which the reserve is there to protect.
func watchScratch(ctx context.Context, tick <-chan time.Time, startFree, limit int64, free func() int64, stop func()) bool {
	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick:
			if overLimit(startFree, free(), limit) {
				stop()
				return true
			}
		}
	}
}

// startScratchWatch watches the filesystem holding store for the life of the
// command. The returned func ends the watch and says whether it stopped the
// run, and with what limit.
func startScratchWatch(store string, stop func()) func() (limit int64, stopped bool) {
	startFree, total, err := freeSpace(store)
	if err != nil {
		return func() (int64, bool) { return 0, false }
	}
	limit := scratchLimit(startFree, total)
	ctx, cancel := context.WithCancel(context.Background())
	t := time.NewTicker(watchEvery)
	done := make(chan bool, 1)
	go func() {
		done <- watchScratch(ctx, t.C, startFree, limit, func() int64 {
			f, _, err := freeSpace(store)
			if err != nil {
				return startFree
			}
			return f
		}, stop)
	}()
	return func() (int64, bool) {
		cancel()
		t.Stop()
		return limit, <-done
	}
}

// scratchStoppedWarning is the note on a run that was cut short because it
// wrote more than its scratch area may use on disk.
func scratchStoppedWarning(limit int64) sandbox.Warning {
	return sandbox.Warning{Text: fmt.Sprintf("stopped: the command wrote more than the sandbox's scratch area may use on disk (%s), so its full effect is unknown", report.HumanBytes(limit))}
}

const lockName = ".lock"

// lockFolder takes an exclusive lock on dir and keeps it until the returned
// file is closed (or the process dies). A sweep removes only folders it can
// lock, which works across PID namespaces, unlike checking the owner pid.
func lockFolder(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("lock the scratch folder %s: %w", dir, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("lock the scratch folder %s: %w", dir, err)
	}
	return f, nil
}

// tryLock opens dir's lock file and tries to take it without waiting. The file
// is nil when there is none (older folders, or one a run has just made and not
// yet locked); otherwise the caller closes it, which drops the lock.
func tryLock(dir string) (f *os.File, locked bool) {
	f, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, false
	}
	return f, syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
