//go:build linux

package overlayfs

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestScratchLimit(t *testing.T) {
	const gib = 1 << 30
	for _, c := range []struct {
		name        string
		free, total int64
		want        int64
	}{
		{"half of free space on a big disk", 100 * gib, 500 * gib, 50 * gib},
		{"half wins while the reserve is still left", 3 * gib, 500 * gib, 3 * gib / 2},
		{"the reserve wins when free space is tight", gib + gib/2, 500 * gib, gib / 2},
		{"a small disk keeps 5%", 10 * gib, 20 * gib, 5 * gib},
		{"nothing to spare", gib / 2, 500 * gib, 0},
	} {
		if got := scratchLimit(c.free, c.total); got != c.want {
			t.Errorf("%s: limit %d, want %d", c.name, got, c.want)
		}
	}
}

func TestOverLimit(t *testing.T) {
	if overLimit(100, 60, 50) {
		t.Error("40 written, limit 50: not over")
	}
	if overLimit(100, 50, 50) {
		t.Error("exactly the limit is not over")
	}
	if !overLimit(100, 49, 50) {
		t.Error("51 written, limit 50: over")
	}
	if overLimit(100, 150, 50) {
		t.Error("free space grew: not over")
	}
}

func TestWatchScratchStopsOnceWhenOverLimit(t *testing.T) {
	tick := make(chan time.Time)
	frees := []int64{100, 80, 40} // the third check is over a limit of 50
	i, stops := 0, 0
	done := make(chan bool)
	go func() {
		done <- watchScratch(context.Background(), tick, 100, 50, func() int64 {
			f := frees[i]
			i++
			return f
		}, func() { stops++ })
	}()
	for range frees {
		tick <- time.Time{}
	}
	if !<-done {
		t.Error("the watch did not report that it stopped the run")
	}
	if stops != 1 || i != 3 {
		t.Errorf("stops %d after %d checks, want 1 after 3", stops, i)
	}
}

func TestWatchScratchEndsQuietlyWhenTheRunEnds(t *testing.T) {
	tick := make(chan time.Time)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() {
		done <- watchScratch(ctx, tick, 100, 50, func() int64 { return 90 }, func() { t.Error("stopped a small run") })
	}()
	tick <- time.Time{}
	cancel()
	if <-done {
		t.Error("reported a stop for a run that was fine")
	}
}

func TestStartScratchWatchOnARealFolder(t *testing.T) {
	end := startScratchWatch(t.TempDir(), func() { t.Error("stopped a run that wrote nothing") })
	time.Sleep(2 * watchEvery) // long enough for a check
	limit, stopped := end()
	if stopped || limit <= 0 {
		t.Errorf("limit %d stopped %v", limit, stopped)
	}
	// A folder that vanishes mid-run is not a reason to stop it.
	gone := t.TempDir() + "/store"
	os.Mkdir(gone, 0o700)
	end = startScratchWatch(gone, func() { t.Error("stopped a run whose folder could not be measured") })
	os.Remove(gone)
	time.Sleep(2 * watchEvery)
	if _, stopped := end(); stopped {
		t.Error("stopped")
	}
	end = startScratchWatch("/definitely/not/here", func() {})
	if limit, stopped := end(); limit != 0 || stopped {
		t.Errorf("a folder that cannot be measured: %d %v", limit, stopped)
	}
	if _, _, err := freeSpace("/definitely/not/here"); err == nil {
		t.Error("no error for a missing folder")
	}
}

func TestScratchStoppedWarningSaysWhy(t *testing.T) {
	w := scratchStoppedWarning(3 << 30)
	for _, want := range []string{"stopped:", "scratch area", "on disk", "3.0 GiB"} {
		if !strings.Contains(w.Text, want) {
			t.Errorf("%q lacks %q", w.Text, want)
		}
	}
}
