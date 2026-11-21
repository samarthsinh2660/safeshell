package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

type fakeSizer struct {
	size  sandbox.Size
	paths []string
}

func (f *fakeSizer) Size(_ context.Context, paths []string) sandbox.Size {
	f.paths = paths
	return f.size
}

const gib = 1 << 30

func TestARunThatFitsInMemoryIsSimulatedAsBefore(t *testing.T) {
	sim := &fakeSim{}
	z := &fakeSizer{size: sandbox.Size{Bytes: 1 << 20, RAMLimit: 2 * gib}}
	(&Engine{Sim: sim, Sizer: z}).Analyze(context.Background(), "rsync -a --delete src/ /dst/", "/w")
	if len(sim.calls) != 1 || sim.calls[0].UpperOnDisk {
		t.Fatalf("calls %+v", sim.calls)
	}
	if len(z.paths) != 1 || z.paths[0] != "/w/src" {
		t.Errorf("sized %v, want the copy's source", z.paths)
	}
}

func TestABigRunUsesTheDiskWhenItHasRoom(t *testing.T) {
	sim := &fakeSim{}
	z := &fakeSizer{size: sandbox.Size{Bytes: 3 * gib, RAMLimit: 2 * gib, DiskFree: 100 * gib}}
	a := (&Engine{Sim: sim, Sizer: z}).Analyze(context.Background(), "mystery-tool --go", "/w")
	if len(sim.calls) != 1 || !sim.calls[0].UpperOnDisk || a.Sim == nil {
		t.Fatalf("calls %+v", sim.calls)
	}
	if len(z.paths) != 1 || z.paths[0] != "/w" {
		t.Errorf("sized %v, want the folder it runs in", z.paths)
	}
}

func TestABigRunWithNoRoomIsSkippedAndNeverAutoRuns(t *testing.T) {
	for _, line := range []string{"rsync -a --delete src/ /dst/", "mystery-tool --go"} {
		sim := &fakeSim{}
		z := &fakeSizer{size: sandbox.Size{Bytes: 5 * gib, Cut: true, RAMLimit: 2 * gib, DiskFree: gib}}
		a := (&Engine{Sim: sim, Sizer: z}).Analyze(context.Background(), line, "/w")
		fs := find(a, domain.EffectFS)
		if len(sim.calls) != 0 || a.Sim != nil {
			t.Errorf("%q: the sandbox ran", line)
		}
		if fs == nil || fs.Evidence != domain.Estimated || a.Decision == domain.Allow {
			t.Errorf("%q: %+v decision %s: a skipped run must stay estimated and need a person", line, fs, a.Decision)
		}
		if !strings.Contains(a.SimNote, "not simulated") || !strings.Contains(a.SimNote, "at least 5.0 GiB") || !strings.Contains(a.SimNote, "Free some disk space") {
			t.Errorf("%q: note %q", line, a.SimNote)
		}
	}
	// Without the cut the size is exact.
	z := &fakeSizer{size: sandbox.Size{Bytes: 5 * gib, RAMLimit: 2 * gib}}
	if a := (&Engine{Sim: &fakeSim{}, Sizer: z}).Analyze(context.Background(), "mystery-tool --go", "/w"); strings.Contains(a.SimNote, "at least") {
		t.Errorf("note %q", a.SimNote)
	}
}

func TestADeleteIsNeverSized(t *testing.T) {
	sim := &fakeSim{}
	z := &fakeSizer{size: sandbox.Size{RAMLimit: 2 * gib}}
	(&Engine{Sim: sim, Sizer: z}).Analyze(context.Background(), "rm -rf big", "/w")
	if len(sim.calls) != 1 || len(z.paths) != 0 {
		t.Errorf("a delete copies no data: calls %d, sized %v", len(sim.calls), z.paths)
	}
}
