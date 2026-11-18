package integration

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A pipe and an unreadable folder inside a deleted folder must not stop the snapshot:
// undo brings back the files, recreates the pipe and says what was left out.
func TestSnapshotSurvivesAPipeAndAnUnreadableFolder(t *testing.T) {
	needSandbox(t)
	if os.Geteuid() == 0 {
		t.Skip("root ignores permissions")
	}
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	locked := filepath.Join(victim, "locked")
	_ = os.MkdirAll(locked, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	if err := syscall.Mkfifo(filepath.Join(victim, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(locked, 0o000)
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	sh := startBash(t)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("d\r")
	sh.waitFor(t, "Type yes")
	sh.send("yes\r")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(victim); err != nil {
			break
		}
	}
	_ = os.Chmod(locked, 0o755) // lets the test clean up if the command could not remove it

	sh.send("safeshell undo\r")
	sh.waitFor(t, "could not be read")
	sh.send("yes\r")
	sh.waitFor(t, "Verified")
	if b, err := os.ReadFile(filepath.Join(victim, "a.txt")); err != nil || string(b) != "x" {
		t.Errorf("undo did not restore the file: %v %q", err, b)
	}
	if fi, err := os.Lstat(filepath.Join(victim, "pipe")); err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("undo did not recreate the pipe: %v", err)
	}
}
