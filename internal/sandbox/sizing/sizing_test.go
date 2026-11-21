package sizing

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func sparse(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

func TestWalkAddsUpFilesAndNeverFollowsSymlinks(t *testing.T) {
	d := t.TempDir()
	sparse(t, filepath.Join(d, "a/one"), 1000)
	sparse(t, filepath.Join(d, "a/sub/two"), 500)
	outside := t.TempDir()
	sparse(t, filepath.Join(outside, "huge"), 1<<40)
	if err := os.Symlink(outside, filepath.Join(d, "a/link")); err != nil {
		t.Fatal(err)
	}
	// A symlink given as a path is not followed either.
	if err := os.Symlink(outside, filepath.Join(d, "top")); err != nil {
		t.Fatal(err)
	}
	got, cut := Walk(context.Background(), []string{filepath.Join(d, "a"), filepath.Join(d, "a/sub"), filepath.Join(d, "top"), filepath.Join(d, "missing")}, 1<<50, 100, time.Minute)
	if got != 1500 || cut {
		t.Fatalf("got %d, cut %v; want 1500, false (a/sub counted once, links not followed)", got, cut)
	}
}

func TestWalkStopsOverTheLimit(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d"} {
		sparse(t, filepath.Join(d, n), 1000)
	}
	got, cut := Walk(context.Background(), []string{d}, 1500, 100, time.Minute)
	if !cut || got < 1500 || got > 2000 {
		t.Fatalf("got %d, cut %v; want a stop soon after 1500", got, cut)
	}
}

func TestWalkStopsAtItsBudget(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a", "b", "c", "d"} {
		sparse(t, filepath.Join(d, n), 1)
	}
	if _, cut := Walk(context.Background(), []string{d}, 1<<50, 3, time.Minute); !cut {
		t.Error("the file count budget did not stop the walk")
	}
	if _, cut := Walk(context.Background(), []string{d}, 1<<50, 100, -time.Second); !cut {
		t.Error("the time budget did not stop the walk")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, cut := Walk(ctx, []string{d}, 1<<50, 100, time.Minute); !cut {
		t.Error("a cancelled request did not stop the walk")
	}
	// Two folders: the second is never reached once the first used the budget.
	e := t.TempDir()
	sparse(t, filepath.Join(e, "x"), 1)
	if got, cut := Walk(context.Background(), []string{d, e}, 1<<50, 4, time.Minute); !cut || got != 3 {
		t.Errorf("got %d, cut %v; want 3, true", got, cut)
	}
}

func TestRAMLimit(t *testing.T) {
	const g = 1 << 30
	for _, tc := range []struct {
		name, meminfo string
		want          int64
	}{
		{"half of what is available", "MemTotal: 1\nMemAvailable:    2097152 kB\n", g},
		{"capped", "MemAvailable: 99999999 kB\n", 2 * g},
		{"missing", "MemTotal: 1 kB\n", 2 * g},
		{"empty value", "MemAvailable:\n", 2 * g},
		{"not a number", "MemAvailable: lots kB\n", 2 * g},
		{"unreadable", "", 2 * g},
	} {
		if got := RAMLimit(tc.meminfo); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestSizeReportsDataAndRoom(t *testing.T) {
	d := t.TempDir()
	sparse(t, filepath.Join(d, "tree/big"), 5<<30)
	mem := filepath.Join(d, "meminfo")
	if err := os.WriteFile(mem, []byte("MemAvailable: 1048576 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Sizer{Dir: filepath.Join(d, "not/yet/made"), Meminfo: mem}.Size(context.Background(), []string{filepath.Join(d, "tree")})
	if s.Bytes != 5<<30 || s.RAMLimit != 512<<20 || s.DiskFree <= 0 {
		t.Fatalf("got %+v", s)
	}
	none := Sizer{Meminfo: mem, MaxFiles: 1, MaxTime: time.Minute}.Size(context.Background(), []string{filepath.Join(d, "tree")})
	if none.DiskFree != 0 || !none.Cut {
		t.Fatalf("with no disk place and a tiny budget, got %+v", none)
	}
}

func TestFreeSpaceLooksUpwardAndGivesUp(t *testing.T) {
	asked := 0
	got := freeSpace("/a/b/c", func(p string, st *syscall.Statfs_t) error {
		asked++
		if p != "/a" {
			return syscall.ENOENT
		}
		st.Bavail, st.Bsize = 10, 4096
		return nil
	})
	if got != 40960 || asked != 3 {
		t.Fatalf("got %d after %d tries, want 40960 after 3", got, asked)
	}
	if got := freeSpace("/a/b", func(string, *syscall.Statfs_t) error { return syscall.ENOENT }); got != 0 {
		t.Fatalf("got %d, want 0 when no folder can be read", got)
	}
}

func TestSizeReadsTheRealMemoryInfoByDefault(t *testing.T) {
	if s := (Sizer{}).Size(context.Background(), nil); s.RAMLimit <= 0 || s.DiskFree != 0 || s.Bytes != 0 {
		t.Fatalf("got %+v", s)
	}
}
