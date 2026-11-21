//go:build linux

package overlayfs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupRemovesFoldersWithNoPermissions(t *testing.T) {
	root := filepath.Join(t.TempDir(), "run")
	for _, d := range []string{"work/work", "upper/a/b"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, root, "upper/a/b/f", "x")
	for _, d := range []string{"upper/a/b", "upper/a", "work/work"} { // deepest first, so each can still be changed
		if err := os.Chmod(filepath.Join(root, d), 0); err != nil {
			t.Fatal(err)
		}
	}
	cleanup(root)
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Errorf("the run folder is still there: %v", err)
	}
}
