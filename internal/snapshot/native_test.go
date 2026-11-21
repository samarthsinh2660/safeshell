package snapshot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeFS stands in for a btrfs subvolume or a zfs dataset rooted at root: the
// fake tools copy the folder, which is what a read-only snapshot looks like.
type fakeFS struct {
	t      *testing.T
	root   string
	magic  int64
	id     [2]int32
	calls  []string
	failOn string            // a tool command that fails, e.g. "btrfs subvolume snapshot"
	list   string            // what `zfs list` prints; "" means "tank/home\t<root>"
	devs   map[string]uint64 // device of a path and everything under it; the default is 1
	uid    int               // who runs; 0 is root
	mounts string            // /proc/self/mountinfo; "" means btrfs mounted with user_subvol_rm_allowed
	late   time.Duration     // a zfs snapshot folder shows up this long after `zfs snapshot`
	never  bool              // ... or never
}

func (f *fakeFS) host() Host {
	return Host{
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			cmd := name + " " + strings.Join(args, " ")
			f.calls = append(f.calls, cmd)
			if f.failOn != "" && strings.HasPrefix(cmd, f.failOn) {
				return []byte("ERROR: Operation not permitted\nmore"), errors.New("exit status 1")
			}
			switch {
			case strings.HasPrefix(cmd, "btrfs subvolume snapshot -r "):
				f.copyTo(args[len(args)-1])
			case name == "zfs" && args[0] == "snapshot":
				_, snap, _ := strings.Cut(args[1], "@")
				dest := filepath.Join(f.root, ".zfs", "snapshot", snap)
				switch {
				case f.never:
				case f.late > 0:
					time.AfterFunc(f.late, func() { f.copyTo(dest) })
				default:
					f.copyTo(dest)
				}
			case name == "zfs" && args[0] == "list":
				if f.list != "" {
					return []byte(f.list), nil
				}
				return []byte("tank/home\t" + f.root + "\n"), nil
			case strings.HasPrefix(cmd, "btrfs subvolume delete "), name == "zfs" && args[0] == "destroy":
				if name == "btrfs" {
					os.RemoveAll(args[len(args)-1])
				}
			}
			return nil, nil
		},
		Statfs: func(string) (Fs, error) { return Fs{Type: f.magic, ID: f.id}, nil },
		Ino: func(p string) (uint64, error) {
			if p == f.root {
				return btrfsSubvolIno, nil
			}
			return OS().Ino(p)
		},
		Dev: func(p string) (uint64, error) {
			best, dev := -1, uint64(1)
			for q, d := range f.devs {
				if (p == q || strings.HasPrefix(p, q+"/")) && len(q) > best {
					best, dev = len(q), d
				}
			}
			return dev, nil
		},
		Euid: func() int { return f.uid },
		Mounts: func() ([]byte, error) {
			if f.mounts == "" {
				return []byte("36 25 0:30 / " + f.root + " rw - btrfs /dev/loop0 rw,user_subvol_rm_allowed,subvolid=256\n"), nil
			}
			return []byte(f.mounts), nil
		},
	}
}

func (f *fakeFS) copyTo(dest string) {
	f.t.Helper()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		f.t.Fatal(err)
	}
	des, _ := os.ReadDir(f.root)
	for _, de := range des {
		if de.Name() == ".zfs" {
			continue
		}
		if out, err := exec.Command("cp", "-a", filepath.Join(f.root, de.Name()), dest).CombinedOutput(); err != nil {
			f.t.Fatalf("cp: %v %s", err, out)
		}
	}
}

func newFake(t *testing.T, magic int64) (*fakeFS, string, string) {
	root, store := t.TempDir(), t.TempDir()
	write(t, filepath.Join(root, "proj/a.txt"), "alpha", 0o644)
	write(t, filepath.Join(root, "proj/sub/b.sh"), "#!/bin/sh", 0o755)
	if err := os.Symlink("a.txt", filepath.Join(root, "proj/link")); err != nil {
		t.Fatal(err)
	}
	return &fakeFS{t: t, root: root, magic: magic}, root, store
}

func TestNativeSnapshotsRestoreTouchedPathsOnly(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		magic int64
	}{{"btrfs", btrfsMagic}, {"zfs", zfsMagic}} {
		t.Run(tc.kind, func(t *testing.T) {
			f, root, store := newFake(t, tc.magic)
			proj := filepath.Join(root, "proj")
			created := filepath.Join(root, "new.txt")
			other := filepath.Join(root, "other.txt")
			write(t, other, "untouched", 0o644)

			m, err := CreateWith(f.host(), store, Origin{}, "rm -rf proj", []Target{{Path: proj}, {Path: created, Create: true}}, 1)
			if err != nil {
				t.Fatal(err)
			}
			if m.Native == nil || m.Native.Kind != tc.kind || m.Bytes != 0 {
				t.Fatalf("native %+v bytes %d", m.Native, m.Bytes)
			}
			if _, err := os.Stat(filepath.Join(store, m.ID, "blobs", "x")); err == nil {
				t.Error("no file data should be copied")
			}
			loaded, err := Load(store, m.ID)
			if err != nil || loaded.Native == nil || *loaded.Native != *m.Native {
				t.Fatalf("manifest round trip: %v %+v", err, loaded.Native)
			}

			os.RemoveAll(proj)
			write(t, created, "stray", 0o644)
			write(t, other, "changed later", 0o644) // not touched by the command: undo must leave it

			r, err := Restore(store, m.ID)
			if err != nil || len(r.Problems) != 0 {
				t.Fatalf("restore: %v %v", err, r.Problems)
			}
			if read(t, filepath.Join(proj, "a.txt")) != "alpha" || read(t, filepath.Join(proj, "sub/b.sh")) != "#!/bin/sh" {
				t.Error("files not restored")
			}
			if l, _ := os.Readlink(filepath.Join(proj, "link")); l != "a.txt" {
				t.Errorf("link %q", l)
			}
			if _, err := os.Lstat(created); err == nil {
				t.Error("created file should be removed")
			}
			if read(t, other) != "changed later" {
				t.Error("an untouched path was changed")
			}
			// Changing a restored file is noticed by verify.
			write(t, filepath.Join(proj, "a.txt"), "beta", 0o644)
			if !hasProblem(Verify(store, m), "different contents") {
				t.Error("verify should notice")
			}
		})
	}
}

func TestNativeFallsBackToCopyWhenTheToolFails(t *testing.T) {
	for _, tc := range []struct {
		magic  int64
		failOn string
	}{{btrfsMagic, "btrfs subvolume snapshot"}, {zfsMagic, "zfs snapshot"}} {
		f, root, store := newFake(t, tc.magic)
		f.failOn = tc.failOn
		m, err := CreateWith(f.host(), store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
		if err != nil || m.Native != nil || m.Bytes != int64(len("alpha#!/bin/sh")) {
			t.Fatalf("%s: want a copy snapshot: %v %+v", tc.failOn, err, m)
		}
		os.RemoveAll(filepath.Join(root, "proj"))
		if r, err := Restore(store, m.ID); err != nil || len(r.Problems) != 0 {
			t.Fatalf("restore: %v %v", err, r.Problems)
		}
	}
}

func TestNativeWithUnusableStoreReportsTheCopyError(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	if err := os.WriteFile(filepath.Join(store, "x"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// The store is a file: no folder can be made, so the copy backend reports the error.
	if _, err := CreateWith(f.host(), filepath.Join(store, "x"), Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0); err == nil {
		t.Error("an unusable store must fail")
	}
}

func TestNativeCleanupWhenBlobsFolderCannotBeMade(t *testing.T) {
	f, root, store := newFake(t, zfsMagic)
	h := f.host()
	run := h.Run
	h.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := run(ctx, name, args...)
		if name == "zfs" && args[0] == "snapshot" {
			// The snapshot exists; now a file takes the place of the blobs folder.
			dirs, _ := os.ReadDir(store)
			for _, d := range dirs {
				os.WriteFile(filepath.Join(store, d.Name(), "blobs"), nil, 0o600)
			}
		}
		return out, err
	}
	_, err := CreateWith(h, store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "zfs destroy tank/home@safeshell-") {
		t.Errorf("the native snapshot should be destroyed: %v", f.calls)
	}
	// When it cannot be destroyed, the error says so instead of hiding it.
	f, root, store = newFake(t, zfsMagic)
	f.failOn = "zfs destroy"
	h = f.host()
	run = h.Run
	h.Run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		out, err := run(ctx, name, args...)
		if name == "zfs" && args[0] == "snapshot" {
			dirs, _ := os.ReadDir(store)
			for _, d := range dirs {
				os.WriteFile(filepath.Join(store, d.Name(), "blobs"), nil, 0o600)
			}
		}
		return out, err
	}
	if _, err = CreateWith(h, store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0); err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Errorf("want the failed destroy reported: %v", err)
	}
	if des, _ := os.ReadDir(store); len(des) != 0 {
		t.Errorf("nothing should be left in the store: %v", des)
	}
}

func TestChooseNeedsOneFilesystemUnderEveryTarget(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	proj := Target{Path: filepath.Join(root, "proj")}
	h := f.host()
	if h.Choose(store, nil) != nil {
		t.Error("no targets, no native")
	}
	if h.Choose(store, []Target{proj, {Path: filepath.Join(root, "proj/new/deeper"), Create: true}}) == nil {
		t.Error("a path that does not exist yet is covered by its parent")
	}
	if h.Backend(store, proj.Path) != "btrfs" {
		t.Error("backend name")
	}

	mixed := h
	mixed.Statfs = func(p string) (Fs, error) {
		if strings.HasSuffix(p, "sub") {
			return Fs{Type: btrfsMagic, ID: [2]int32{1, 1}}, nil
		}
		return Fs{Type: btrfsMagic}, nil
	}
	if mixed.Choose(store, []Target{proj, {Path: filepath.Join(root, "proj/sub")}}) != nil {
		t.Error("two filesystems must use copy")
	}
	other := h
	other.Statfs = func(p string) (Fs, error) {
		if strings.HasPrefix(p, store) {
			return Fs{Type: btrfsMagic, ID: [2]int32{9, 9}}, nil
		}
		return Fs{Type: btrfsMagic}, nil
	}
	if other.Choose(store, []Target{proj}) != nil {
		t.Error("a store on another filesystem must use copy")
	}
	noStore := h
	noStore.Statfs = func(p string) (Fs, error) {
		if strings.HasPrefix(p, store) {
			return Fs{}, errors.New("gone")
		}
		return Fs{Type: btrfsMagic}, nil
	}
	if noStore.Choose(store, []Target{proj}) != nil {
		t.Error("an unreadable store must use copy")
	}
	ext4 := h
	ext4.Statfs = func(string) (Fs, error) { return Fs{Type: 0xEF53}, nil }
	if ext4.Choose(store, []Target{proj}) != nil || ext4.Backend(store, proj.Path) != "copy" {
		t.Error("ext4 is the copy backend")
	}
	broken := h
	broken.Statfs = func(string) (Fs, error) { return Fs{}, errors.New("no") }
	if broken.Choose(store, []Target{proj}) != nil {
		t.Error("statfs failure is the copy backend")
	}
}

func TestChooseBtrfsSubvolumes(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	h := f.host()
	sub := filepath.Join(root, "proj/sub")
	if h.Choose(store, []Target{{Path: filepath.Join(root, "proj/a.txt")}, {Path: sub}}) == nil {
		t.Error("one subvolume")
	}
	// A nested subvolume: sub has its own top folder, so the targets span two subvolumes.
	nested := h
	nested.Ino = func(p string) (uint64, error) {
		if p == sub {
			return btrfsSubvolIno, nil
		}
		return h.Ino(p)
	}
	if nested.Choose(store, []Target{{Path: filepath.Join(root, "proj/a.txt")}, {Path: filepath.Join(sub, "b.sh")}}) != nil {
		t.Error("two subvolumes must use copy")
	}
	// A path that disappears while the subvolume is being looked for.
	vanishing, calls := h, 0
	vanishing.Ino = func(p string) (uint64, error) {
		if calls++; calls > 1 {
			return 0, errors.New("gone")
		}
		return h.Ino(p)
	}
	if vanishing.Choose(store, []Target{{Path: filepath.Join(root, "proj")}}) != nil {
		t.Error("a vanished path: copy")
	}
	// No subvolume top found on the way up.
	none := h
	none.Ino = OS().Ino
	if none.Choose(store, []Target{{Path: filepath.Join(root, "proj")}}) != nil {
		t.Error("no subvolume top: copy")
	}
}

func TestChooseZfsRequiresAMountedDatasetCoveringEveryPath(t *testing.T) {
	f, root, store := newFake(t, zfsMagic)
	targets := []Target{{Path: filepath.Join(root, "proj")}}
	for _, tc := range []struct {
		name, list string
		want       bool
	}{
		{"ok", "", true},
		{"legacy", "tank/home\tlegacy\n", false},
		{"none", "tank/home\tnone\n", false},
		{"garbage", "nothing useful", false},
		{"no name", "\t" + root + "\n", false},
		{"elsewhere", "tank/home\t/some/other/place\n", false},
		{"deeper", "tank/home\t" + filepath.Join(root, "proj/sub") + "\n", false},
	} {
		f.list = tc.list
		if got := f.host().Choose(store, targets) != nil; got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	f.list = ""
	f.failOn = "zfs list"
	if f.host().Choose(store, targets) != nil {
		t.Error("no zfs tool: copy")
	}
	f.failOn = ""
	two := f.host()
	n := 0
	two.Statfs = func(string) (Fs, error) { n++; return Fs{Type: zfsMagic, ID: [2]int32{int32(n), 0}}, nil }
	if two.Choose(store, []Target{targets[0], {Path: filepath.Join(root, "proj/sub")}}) != nil {
		t.Error("two datasets: copy")
	}
}

func TestNativeRefMapsPaths(t *testing.T) {
	r := NativeRef{Root: "/home/u", Path: "/store/id/snap"}
	if got := r.src("/home/u/proj/a"); got != "/store/id/snap/proj/a" {
		t.Errorf("src %q", got)
	}
	if got := r.live("/store/id/snap/proj/a"); got != "/home/u/proj/a" {
		t.Errorf("live %q", got)
	}
	if r.live(r.Path) != r.Root || r.src(r.Root) != r.Path {
		t.Error("roots map to each other")
	}
}

func TestDeleteNative(t *testing.T) {
	f, root, _ := newFake(t, btrfsMagic)
	h := f.host()
	if err := h.Delete(NativeRef{Kind: "btrfs", Path: filepath.Join(root, "x")}); err != nil {
		t.Fatal(err)
	}
	if err := h.Delete(NativeRef{Kind: "zfs", Name: "tank/home@safeshell-1"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, "|"); !strings.Contains(got, "btrfs subvolume delete "+root+"/x") || !strings.Contains(got, "zfs destroy tank/home@safeshell-1") {
		t.Errorf("calls %s", got)
	}
	if err := h.Delete(NativeRef{Kind: "ext4"}); err == nil {
		t.Error("unknown kind")
	}
	f.failOn = "btrfs subvolume delete"
	gone := filepath.Join(root, "gone")
	if err := h.Delete(NativeRef{Kind: "btrfs", Path: gone}); err != nil {
		t.Errorf("a snapshot that is already gone is fine: %v", err)
	}
	err := h.Delete(NativeRef{Kind: "btrfs", Path: root})
	if err == nil || !strings.Contains(err.Error(), "Operation not permitted") || strings.Contains(err.Error(), "more") {
		t.Errorf("want the first line of the tool's message: %v", err)
	}
	if firstLine(nil, errors.New("boom")) != "boom" {
		t.Error("an empty output falls back to the error")
	}
}

func TestPruneDeletesNativeSnapshotsFirst(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	h := f.host()
	m, err := CreateWith(h, store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	if err != nil || m.Native == nil {
		t.Fatalf("%v %+v", err, m)
	}
	f.calls = nil
	r, err := prune(h, store, time.Nanosecond, 0, "")
	if err != nil || r.Removed != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if len(f.calls) != 2 || !strings.HasPrefix(f.calls[0], "btrfs property set -ts ") || !strings.HasPrefix(f.calls[1], "btrfs subvolume delete ") {
		t.Errorf("calls %v", f.calls)
	}
	if _, err := os.Stat(dir(store, m.ID)); err == nil {
		t.Error("the snapshot folder should be gone")
	}

	// A snapshot that cannot be deleted stays, and prune says so.
	m, _ = CreateWith(h, store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	f.failOn = "btrfs subvolume delete"
	if _, err := prune(h, store, time.Nanosecond, 0, ""); err == nil {
		t.Error("want the delete error")
	}
	if _, err := Load(store, m.ID); err != nil {
		t.Errorf("the snapshot must stay: %v", err)
	}
}

func TestOSHostDevEuidAndMounts(t *testing.T) {
	h := OS()
	d := t.TempDir()
	if dev, err := h.Dev(d); err != nil || dev == 0 {
		t.Errorf("dev %d %v", dev, err)
	}
	if _, err := h.Dev(filepath.Join(d, "none")); err == nil {
		t.Error("dev of a missing path")
	}
	if h.Euid() != os.Geteuid() {
		t.Error("euid")
	}
	if b, err := h.Mounts(); err != nil || len(b) == 0 {
		t.Errorf("mounts %v", err)
	}
}

func TestOSHost(t *testing.T) {
	h := OS()
	d := t.TempDir()
	if _, err := h.Statfs(d); err != nil {
		t.Error(err)
	}
	if _, err := h.Statfs(filepath.Join(d, "none")); err == nil {
		t.Error("statfs of a missing path")
	}
	if ino, err := h.Ino(d); err != nil || ino == 0 {
		t.Errorf("ino %d %v", ino, err)
	}
	if _, err := h.Ino(filepath.Join(d, "none")); err == nil {
		t.Error("ino of a missing path")
	}
	if out, err := h.Run(context.Background(), "echo", "hi"); err != nil || string(out) != "hi\n" {
		t.Errorf("run %q %v", out, err)
	}
	if _, err := h.Run(context.Background(), "safeshell-no-such-tool"); err == nil {
		t.Error("a missing tool is an error")
	}
	// Prune and Backend work on the real machine whatever the filesystem is.
	if _, err := Prune(d, 0, 0, ""); err != nil {
		t.Error(err)
	}
	if b := h.Backend(d, d); b != "copy" && b != "btrfs" && b != "zfs" {
		t.Errorf("backend %q", b)
	}
}

func TestNestedSubvolumeOrChildDatasetMeansCopy(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		magic int64
	}{{"btrfs", btrfsMagic}, {"zfs", zfsMagic}} {
		t.Run(tc.kind, func(t *testing.T) {
			f, root, store := newFake(t, tc.magic)
			write(t, filepath.Join(root, "proj/sub/deep/c.txt"), "child", 0o644)
			f.devs = map[string]uint64{filepath.Join(root, "proj/sub"): 2} // a nested subvolume or child dataset
			proj := filepath.Join(root, "proj")

			if f.host().Backend(store, proj) != "copy" {
				t.Error("something under the folder is on another device: copy")
			}
			m, err := CreateWith(f.host(), store, Origin{}, "rm -rf proj", []Target{{Path: proj}}, 0)
			if err != nil || m.Native != nil || m.Bytes == 0 {
				t.Fatalf("want a copy snapshot: %v %+v", err, m)
			}
			for _, c := range f.calls {
				if strings.Contains(c, "snapshot") {
					t.Errorf("no native snapshot should be taken: %v", f.calls)
				}
			}
			os.RemoveAll(proj)
			if r, err := Restore(store, m.ID); err != nil || len(r.Problems) != 0 {
				t.Fatalf("restore: %v %v", err, r.Problems)
			}
			if read(t, filepath.Join(proj, "sub/deep/c.txt")) != "child" {
				t.Error("the nested files must be restored")
			}

			// The same folder without the nested device is covered natively.
			f.devs = nil
			if f.host().Backend(store, proj) != tc.kind {
				t.Error("one device: native")
			}
			// A target that is itself on another device than the snapshot's root.
			f.devs = map[string]uint64{proj: 2}
			if f.host().Backend(store, proj) != "copy" {
				t.Error("the target itself on another device: copy")
			}
		})
	}
}

func TestCrossesDevicesTreatsUnknownAsCrossing(t *testing.T) {
	f, root, _ := newFake(t, btrfsMagic)
	h := f.host()
	proj := filepath.Join(root, "proj")
	if h.crossesDevices(root, []string{proj, filepath.Join(root, "missing")}) {
		t.Error("a missing path is nothing to cross")
	}
	noDev := h
	noDev.Dev = func(string) (uint64, error) { return 0, errors.New("no") }
	if !noDev.crossesDevices(root, []string{proj}) {
		t.Error("an unreadable device number counts")
	}
	if os.Geteuid() == 0 {
		return // root reads every folder
	}
	sub := filepath.Join(proj, "sub")
	if err := os.Chmod(sub, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(sub, 0o755) })
	if !h.crossesDevices(root, []string{proj}) {
		t.Error("a folder that cannot be read counts")
	}
}

func TestBtrfsNeedsUserSubvolRmAllowedForNonRoot(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	proj := []Target{{Path: filepath.Join(root, "proj")}}
	f.uid = 1000
	if f.host().Choose(store, proj) == nil {
		t.Error("mounted with user_subvol_rm_allowed: native")
	}
	for name, mounts := range map[string]string{
		"missing option": "36 25 0:30 / " + root + " rw - btrfs /dev/loop0 rw,subvolid=256\n",
		"other mount":    "36 25 0:30 / /elsewhere rw - btrfs /dev/loop0 rw,user_subvol_rm_allowed\n",
		"not mountinfo":  "garbage\n\n",
		"inner mount wins": "1 0 0:1 / / rw - btrfs /dev/a rw,user_subvol_rm_allowed\n" +
			"36 1 0:30 / " + root + " rw - btrfs /dev/loop0 rw,subvolid=256\n",
	} {
		f.mounts = mounts
		if f.host().Choose(store, proj) != nil {
			t.Errorf("%s: copy", name)
		}
	}
	f.mounts = "1 0 0:1 / / rw - btrfs /dev/a rw,user_subvol_rm_allowed\n" +
		"36 1 0:30 / /mnt/with\\040space rw - btrfs /dev/b rw\n"
	if f.host().Choose(store, proj) == nil {
		t.Error("the mount that holds the folder has the option: native")
	}
	h := f.host()
	h.Mounts = func() ([]byte, error) { return nil, errors.New("no") }
	if h.Choose(store, proj) != nil {
		t.Error("unreadable mounts: copy")
	}
	f.uid = 0
	if h.Choose(store, proj) == nil {
		t.Error("root can always delete")
	}
}

func TestPruneKeepsGoingPastASnapshotItCannotDelete(t *testing.T) {
	f, root, store := newFake(t, btrfsMagic)
	h := f.host()
	var ms []Manifest
	for range 3 {
		m, err := CreateWith(h, store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
		if err != nil || m.Native == nil {
			t.Fatalf("%v %+v", err, m)
		}
		ms = append(ms, m)
	}
	f.failOn = "btrfs subvolume delete " + ms[0].Native.Path // the oldest cannot be deleted
	r, err := prune(h, store, time.Nanosecond, 0, "")
	if err == nil || !strings.Contains(err.Error(), "administrator") {
		t.Errorf("want the delete error at the end: %v", err)
	}
	if r.Removed != 2 || r.Left != 1 {
		t.Errorf("the others must still be pruned: %+v", r)
	}
	if _, err := Load(store, ms[0].ID); err != nil {
		t.Errorf("the stuck snapshot must stay: %v", err)
	}
}

func TestZfsWaitsForTheSnapshotFolder(t *testing.T) {
	f, root, store := newFake(t, zfsMagic)
	f.late = 200 * time.Millisecond
	m, err := CreateWith(f.host(), store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	if err != nil || m.Native == nil {
		t.Fatalf("a late folder is waited for: %v %+v", err, m.Native)
	}

	f, root, store = newFake(t, zfsMagic)
	f.never = true
	m, err = CreateWith(f.host(), store, Origin{}, "rm a", []Target{{Path: filepath.Join(root, "proj")}}, 0)
	if err != nil || m.Native != nil || m.Bytes == 0 {
		t.Fatalf("a folder that never shows up falls back to a copy: %v %+v", err, m)
	}
	if !strings.Contains(strings.Join(f.calls, "\n"), "zfs destroy tank/home@safeshell-") {
		t.Errorf("the unreadable snapshot should be destroyed: %v", f.calls)
	}
}
