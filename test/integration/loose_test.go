package integration

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// partlyMirroredDir finds a folder the user can write to that has a mount point
// directly inside it (such as an extra disk under ~). The sandbox cannot overlay
// such a folder as a whole, so its loose files are the case under test.
func partlyMirroredDir(t *testing.T) string {
	t.Helper()
	if d := os.Getenv("SS_PARTLY_DIR"); d != "" { // CI mounts a tmpfs inside it, so the test cannot skip there
		return d
	}
	mi, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Skip("cannot read the mount table")
	}
	for _, line := range strings.Split(string(mi), "\n") {
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		parent := filepath.Dir(f[4])
		if parent == "/" || strings.Contains(f[4], `\`) || strings.ContainsAny(parent, ",:") || syscall.Access(parent, 2) != nil {
			continue
		}
		if top := strings.SplitN(parent, "/", 3)[1]; top == "proc" || top == "sys" || top == "dev" || top == "run" {
			continue
		}
		return parent
	}
	t.Skip("no writable folder with a mount point inside it on this machine")
	return ""
}

func TestLooseFilesInAPartlyMirroredFolderAreObserved(t *testing.T) {
	needSandbox(t)
	dir := partlyMirroredDir(t)
	edit := filepath.Join(dir, "ss-loose-edit.txt")
	gone := filepath.Join(dir, "ss-loose-gone.txt")
	for _, p := range []string{edit, gone} {
		if err := os.WriteFile(p, []byte("data\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(p) })
	}
	line := "echo more >> " + edit + "; rm " + gone + "; echo hi > " + filepath.Join(dir, "ss-loose-new.txt")

	r := simulate(t, "--cwd", dir, line)
	for name, kind := range map[string]string{"ss-loose-edit.txt": "modified", "ss-loose-gone.txt": "deleted", "ss-loose-new.txt": "created"} {
		if c, ok := r.find("/" + name); !ok || c.Kind != kind {
			t.Errorf("%s: want %s, got %+v (all: %+v)", name, kind, c, r.Diff.Changes)
		}
	}
	if b, _ := os.ReadFile(edit); string(b) != "data\n" {
		t.Errorf("the real file was changed by the simulation: %q", b)
	}
	if _, err := os.Stat(gone); err != nil {
		t.Errorf("the real file was removed by the simulation: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ss-loose-new.txt")); err == nil {
		t.Error("the simulation created a real file")
	}

	a := analyze(t, "--cwd", dir, line)
	if fs, ok := a.effect("files"); !ok || fs.Evidence != "OBSERVED" {
		t.Errorf("analyze should show the loose-file change as OBSERVED, got %+v", fs)
	}
}

// A partly mirrored folder with no loose files of its own (CI's has only the
// mount point) still reports a file the command creates in it.
func TestNewFileInAPartlyMirroredFolderIsObserved(t *testing.T) {
	needSandbox(t)
	dir := partlyMirroredDir(t)
	newFile := filepath.Join(dir, "ss-loose-only-new.txt")
	r := simulate(t, "--cwd", dir, "echo hi > "+newFile)
	if c, ok := r.find("/ss-loose-only-new.txt"); !ok || c.Kind != "created" {
		t.Errorf("want created, got %+v (all: %+v)", c, r.Diff.Changes)
	}
	if _, err := os.Stat(newFile); err == nil {
		t.Error("the simulation created a real file")
	}
}
