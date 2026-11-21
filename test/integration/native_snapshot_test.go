package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

// On a btrfs subvolume or a ZFS dataset, undo restores from a native snapshot.
// Point SS_BTRFS_DIR at a folder you own inside a btrfs subvolume, or SS_ZFS_DIR
// at one inside a ZFS dataset you may snapshot (zfs allow). Without them, and
// in CI, these tests skip: neither filesystem is available there.
func TestUndoFromNativeSnapshot(t *testing.T) {
	for _, tc := range []struct{ kind, env string }{{"btrfs", "SS_BTRFS_DIR"}, {"zfs", "SS_ZFS_DIR"}} {
		t.Run(tc.kind, func(t *testing.T) {
			needSandbox(t)
			base := os.Getenv(tc.env)
			if base == "" {
				t.Skip(tc.env + " not set: no " + tc.kind + " filesystem to test on")
			}
			work, err := os.MkdirTemp(base, "ss-native-")
			if err != nil {
				t.Fatal(err)
			}
			data := filepath.Join(work, "data")
			t.Cleanup(func() { // a native snapshot is not an ordinary folder: prune deletes it first
				_, _ = snapshot.Prune(filepath.Join(data, "safeshell", "snapshots"), time.Nanosecond, 0, "")
				_ = os.RemoveAll(work)
			})
			victim := filepath.Join(work, "proj")
			_ = os.MkdirAll(victim, 0o755)
			if err := os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}

			sh := startBash(t, "XDG_DATA_HOME="+data)
			sh.waitFor(t, "READY>")
			sh.send(`eval "$(safeshell hook bash)"` + "\r")
			sh.send("rm -rf " + victim + "\r")
			sh.waitFor(t, "[c] cancel")
			sh.send("d\r")
			sh.waitFor(t, "Type yes")
			sh.send("yes\r")
			sh.waitFor(t, tc.kind+", nothing copied")
			for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
				if _, err := os.Stat(victim); err != nil {
					break
				}
			}
			sh.send("safeshell undo\r")
			sh.waitFor(t, "Type yes")
			sh.send("yes\r")
			sh.waitFor(t, "Verified")
			if b, err := os.ReadFile(filepath.Join(victim, "a.txt")); err != nil || string(b) != "x" {
				t.Errorf("undo did not restore the file: %v %q", err, b)
			}
			ms, _ := filepath.Glob(filepath.Join(data, "safeshell", "snapshots", "*", "manifest.json"))
			if len(ms) != 1 {
				t.Fatalf("want one snapshot, found %v", ms)
			}
			if b, _ := os.ReadFile(ms[0]); !strings.Contains(string(b), `"kind": "`+tc.kind+`"`) {
				t.Error("the snapshot should be a native one")
			}
		})
	}
}
