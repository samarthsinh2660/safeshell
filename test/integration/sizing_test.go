package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type sizedAnalysis struct {
	SimNote  string `json:"sim_note"`
	Decision string
	Effects  []struct{ Kind, Evidence string }
	Sim      *struct {
		Diff     struct{ Changes []change }
		Warnings []struct{ Text string }
	}
}

// sizedRun analyzes line in a folder holding the given sparse files (name to
// size), with the on-disk scratch area under its own cache folder. Sparse
// files cost no real disk, but count in full when the run is sized.
func sizedRun(t *testing.T, files map[string]int64, line string) (a sizedAnalysis, tree, cache string, took time.Duration) {
	t.Helper()
	return sizedRunIn(t, t.TempDir(), files, line)
}

// sizedRunIn is sizedRun with the cache folder chosen by the caller.
func sizedRunIn(t *testing.T, cache string, files map[string]int64, line string) (a sizedAnalysis, tree, _ string, took time.Duration) {
	t.Helper()
	tree = t.TempDir()
	for name, size := range files {
		f, err := os.Create(filepath.Join(tree, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil {
			t.Skipf("this filesystem cannot hold a sparse file of %d bytes: %v", size, err)
		}
		f.Close()
	}
	cmd := exec.Command(ssBin, "analyze", "--json", "--cwd", tree, line)
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	start := time.Now()
	out, err := cmd.Output()
	took = time.Since(start)
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatalf("safeshell analyze: %v", err)
	}
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	return a, tree, cache, took
}

func leftovers(t *testing.T, cache string) []string {
	t.Helper()
	m, _ := filepath.Glob(filepath.Join(cache, "safeshell", "sandbox", "*"))
	return m
}

func TestAHugeFolderIsNotSimulatedAndNeedsAPerson(t *testing.T) {
	needSandbox(t)
	const tib = 1 << 40
	a, tree, cache, took := sizedRun(t, map[string]int64{"a.img": 4 * tib, "b.img": 4 * tib}, "chmod -R 600 .")
	if a.Sim != nil {
		t.Fatal("a folder of 8 TiB was simulated")
	}
	if !strings.Contains(a.SimNote, "not simulated") || !strings.Contains(a.SimNote, "Free some disk space") {
		t.Errorf("note %q", a.SimNote)
	}
	if a.Decision == "allow" {
		t.Errorf("a skipped run ran unattended: %s", a.Decision)
	}
	if len(a.Effects) == 0 || a.Effects[len(a.Effects)-1].Evidence != "ESTIMATED" {
		t.Errorf("effects %+v, want an ESTIMATED one", a.Effects)
	}
	if took > 20*time.Second {
		t.Errorf("sizing took %s", took)
	}
	if fi, _ := os.Stat(filepath.Join(tree, "a.img")); fi.Mode().Perm() == 0o600 {
		t.Error("the real file was changed")
	}
	if l := leftovers(t, cache); len(l) != 0 {
		t.Errorf("left behind: %v", l)
	}
}

func TestABigFolderIsSimulatedWithTheScratchAreaOnDisk(t *testing.T) {
	needSandbox(t)
	const gib = 1 << 30
	var st syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &st); err != nil || int64(st.Bavail)*int64(st.Bsize) < 16*gib {
		t.Skip("needs 16 GiB free on the disk that holds the temporary folder")
	}
	files := map[string]int64{}
	for _, n := range []string{"a.img", "b.img", "c.img"} {
		files[n] = gib
	}
	a, tree, cache, _ := sizedRun(t, files, "chmod -R 600 .")
	if a.Sim == nil || strings.Contains(a.SimNote, "not simulated") {
		t.Fatalf("a big folder with free disk was not simulated: %q", a.SimNote)
	}
	if len(a.Sim.Diff.Changes) == 0 {
		t.Error("the simulation saw no changes")
	}
	if fi, _ := os.Stat(filepath.Join(tree, "a.img")); fi.Mode().Perm() == 0o600 {
		t.Error("the real file was changed")
	}
	if l := leftovers(t, cache); len(l) != 0 {
		t.Errorf("left behind: %v", l)
	}
}

// memAvailable is the free memory in bytes, from /proc/meminfo.
func memAvailable(t *testing.T) int64 {
	t.Helper()
	b, _ := os.ReadFile("/proc/meminfo")
	for _, l := range strings.Split(string(b), "\n") {
		var kb int64
		if _, err := fmt.Sscanf(l, "MemAvailable: %d kB", &kb); err == nil {
			return kb << 10
		}
	}
	t.Skip("cannot read MemAvailable")
	return 0
}

// The scratch area on disk may use half the free space (less a reserve). A
// command that writes past that is stopped, and the result says so. CI sets
// SS_SMALL_CACHE_DIR to a small filesystem so that the limit is a few GiB; on a
// dev machine the test runs only when the disk it uses is small enough.
func TestACommandThatFillsTheScratchAreaOnDiskIsStopped(t *testing.T) {
	needSandbox(t)
	const gib = 1 << 30
	small := os.Getenv("SS_SMALL_CACHE_DIR")
	cacheParent := small
	if small == "" {
		cacheParent = t.TempDir()
	}
	cache, err := os.MkdirTemp(cacheParent, "cache-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(cache) })
	var st syscall.Statfs_t
	if err := syscall.Statfs(cache, &st); err != nil {
		t.Fatal(err)
	}
	free, total := int64(st.Bavail)*int64(st.Bsize), int64(st.Blocks)*int64(st.Bsize)
	limit := min(free/2, free-min(int64(gib), total/20))
	mem := memAvailable(t)
	ram := min(mem/2, 2*gib) // the in-memory limit: a source tree above it goes to disk
	size := ram + gib/4
	t.Logf("free %d MiB, scratch limit %d MiB, memory limit %d MiB, source tree %d MiB", free>>20, limit>>20, ram>>20, size>>20)
	ok := limit <= 8*gib && size <= free/2
	if !ok {
		if small != "" && os.Getenv("SS_NEED_SANDBOX") != "" {
			t.Fatal("the numbers do not work out on SS_SMALL_CACHE_DIR (see above)")
		}
		t.Skip("this disk is too big or too small for a quick run of this test")
	}
	// dd keeps to the 1 GiB limit of one file, so write several.
	line := fmt.Sprintf("i=0; while [ $i -lt %d ]; do dd if=/dev/zero of=big$i bs=1M count=1000 status=none; i=$((i+1)); done", limit/gib+2)
	a, tree, _, took := sizedRunIn(t, cache, map[string]int64{"a.img": size}, line)
	// A stopped run keeps no result: it is "not simulated" and needs a person.
	t.Logf("note: %s (took %s)", a.SimNote, took)
	// The reason must be the stopped run, not the size check refusing to simulate at all.
	if a.Sim != nil || !strings.Contains(a.SimNote, "not simulated") || !strings.Contains(a.SimNote, "ran out of space") {
		t.Errorf("a stopped run was reported as simulated: note %q (took %s)", a.SimNote, took)
	}
	var estimated bool
	for _, e := range a.Effects {
		if e.Kind == "files" && e.Evidence == "OBSERVED" {
			t.Errorf("a stopped run still counts as observed: %+v", e)
		}
		estimated = estimated || e.Evidence == "ESTIMATED"
	}
	if !estimated || a.Decision == "allow" {
		t.Errorf("effects %+v, decision %s: want an ESTIMATED effect and no unattended run", a.Effects, a.Decision)
	}
	if took > 30*time.Second {
		t.Errorf("the run took %s", took)
	}
	if _, err := os.Stat(filepath.Join(tree, "big0")); err == nil {
		t.Error("the command wrote a real file")
	}
	if l := leftovers(t, cache); len(l) != 0 {
		t.Errorf("left behind: %v", l)
	}
}
