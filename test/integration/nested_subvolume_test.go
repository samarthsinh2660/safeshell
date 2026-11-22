package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Files in a btrfs subvolume nested inside a mounted one have their own device
// number. Simulating rm there must either show the deletion or say the run was
// not fully simulated; it must never say "changes no files", and the real files
// must stay. Point SS_BTRFS_NESTED_DIR at a folder you own inside such a
// subvolume (the CI native-snapshots job makes one); without it this skips.
func TestNestedSubvolumeSimulationIsHonest(t *testing.T) {
	needSandbox(t)
	base := os.Getenv("SS_BTRFS_NESTED_DIR")
	if base == "" {
		t.Skip("SS_BTRFS_NESTED_DIR not set: no nested btrfs subvolume to test on")
	}
	work, err := os.MkdirTemp(base, "ss-nested-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(work) })
	victim := filepath.Join(work, "proj")
	if err := os.MkdirAll(victim, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(victim, "a.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := analyze(t, "--cwd", work, "rm -rf proj")
	fe, ok := a.effect("files")
	if !ok {
		t.Fatalf("no file effect: %+v", a)
	}
	switch {
	case strings.Contains(fe.Summary, "no files"):
		t.Errorf("the simulation claims the command changes no files: %+v", fe)
	case fe.Evidence == "OBSERVED" && !strings.Contains(fe.Summary, "delete"):
		t.Errorf("observed, but not the deletion: %+v", fe)
	case fe.Evidence != "OBSERVED" && fe.Evidence != "ESTIMATED":
		t.Errorf("unexpected evidence: %+v", fe)
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "x" {
		t.Errorf("the real file was touched: %v %q", err, b)
	}
}
