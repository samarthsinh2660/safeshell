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
	tree, cache = t.TempDir(), t.TempDir()
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
// command that writes past that is stopped, and the result says so.
func TestACommandThatFillsTheScratchAreaOnDiskIsStopped(t *testing.T) {
	needSandbox(t)
	const gib = 1 << 30
	var st syscall.Statfs_t
	if err := syscall.Statfs(os.TempDir(), &st); err != nil {
		t.Skip(err)
	}
	free, total := int64(st.Bavail)*int64(st.Bsize), int64(st.Blocks)*int64(st.Bsize)
	limit := min(free/2, free-min(int64(gib), total/20))
	if limit > 8*gib {
		t.Skipf("the limit here is %d GiB; writing that much would take too long for a test", limit/gib)
	}
	// A sparse tree bigger than the in-memory limit (half of free memory) puts
	// the run on disk; it costs no real space.
	mem := memAvailable(t)
	if mem > free/2 {
		t.Skip("not enough free disk to place a run on disk")
	}
	// dd keeps to the 1 GiB limit of one file, so write several.
	line := fmt.Sprintf("i=0; while [ $i -lt %d ]; do dd if=/dev/zero of=big$i bs=1M count=1000 status=none; i=$((i+1)); done", limit/gib+2)
	a, tree, cache, took := sizedRun(t, map[string]int64{"a.img": mem}, line)
	if a.Sim == nil {
		t.Fatalf("not simulated: %q", a.SimNote)
	}
	var stopped bool
	for _, w := range a.Sim.Warnings {
		stopped = stopped || strings.Contains(w.Text, "stopped: the command wrote more than the sandbox's scratch area may use on disk")
	}
	if !stopped {
		t.Errorf("no stopped warning: %+v (took %s)", a.Sim.Warnings, took)
	}
	if took > 25*time.Second {
		t.Errorf("the run took %s", took)
	}
	if _, err := os.Stat(filepath.Join(tree, "big0")); err == nil {
		t.Error("the command wrote a real file")
	}
	if l := leftovers(t, cache); len(l) != 0 {
		t.Errorf("left behind: %v", l)
	}
}
