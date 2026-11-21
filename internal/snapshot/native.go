package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Filesystem magic numbers reported by statfs.
const (
	btrfsMagic = 0x9123683E
	zfsMagic   = 0x2fc12fc1
)

// btrfsSubvolIno is the inode number of the top folder of every btrfs subvolume.
const btrfsSubvolIno = 256

// toolTimeout bounds one run of btrfs or zfs.
const toolTimeout = 30 * time.Second

// Fs is what statfs says about the filesystem holding a path.
type Fs struct {
	Type int64
	ID   [2]int32 // the same for every path on one btrfs filesystem or one zfs dataset
}

// Host is everything the native backends ask of the machine, so tests can fake it.
type Host struct {
	Run    func(ctx context.Context, name string, args ...string) ([]byte, error) // runs a tool, returns its output
	Statfs func(path string) (Fs, error)
	Ino    func(path string) (uint64, error) // inode number; an error means the path does not exist
}

// OS is the real machine.
func OS() Host {
	return Host{
		Run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, name, args...).CombinedOutput()
		},
		Statfs: func(path string) (Fs, error) {
			var s unix.Statfs_t
			if err := unix.Statfs(path, &s); err != nil {
				return Fs{}, err
			}
			return Fs{Type: int64(s.Type), ID: s.Fsid.Val}, nil
		},
		Ino: func(path string) (uint64, error) {
			var s unix.Stat_t
			if err := unix.Lstat(path, &s); err != nil {
				return 0, err
			}
			return s.Ino, nil
		},
	}
}

// NativeRef records a filesystem snapshot taken for a manifest.
type NativeRef struct {
	Kind string `json:"kind"`           // "btrfs" or "zfs"
	Root string `json:"root"`           // the live folder the snapshot mirrors: the subvolume or the dataset's mount
	Path string `json:"path"`           // where the snapshot's files can be read
	Name string `json:"name,omitempty"` // zfs only: dataset@snapshot
}

// src maps a live path under Root to the same path inside the snapshot.
func (r NativeRef) src(live string) string {
	rel, _ := filepath.Rel(r.Root, live)
	return filepath.Join(r.Path, rel)
}

// live is the inverse of src.
func (r NativeRef) live(src string) string {
	rel, _ := filepath.Rel(r.Path, src)
	return filepath.Join(r.Root, rel)
}

// Native takes a filesystem snapshot that covers every path it was chosen for.
type Native struct {
	Kind string
	take func(ctx context.Context, id, dest string) (NativeRef, error)
}

// create takes the snapshot, or reports why it could not. dest is a path for
// btrfs to create; zfs ignores it.
func (n *Native) create(id, dest string) (NativeRef, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	return n.take(ctx, id, dest)
}

// Delete removes a native snapshot. One that is already gone counts as deleted.
func (h Host) Delete(ref NativeRef) error {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	var out []byte
	var err error
	switch ref.Kind {
	case "btrfs":
		// A read-only subvolume cannot be deleted by its owner (the kernel says "Read-only
		// file system"): make it writable first. If that is not allowed, the delete says so.
		_, _ = h.Run(ctx, "btrfs", "property", "set", "-ts", ref.Path, "ro", "false")
		out, err = h.Run(ctx, "btrfs", "subvolume", "delete", ref.Path)
	case "zfs":
		out, err = h.Run(ctx, "zfs", "destroy", ref.Name)
	default:
		return fmt.Errorf("unknown snapshot kind %q", ref.Kind)
	}
	if err == nil {
		return nil
	}
	if _, serr := os.Lstat(ref.Path); errors.Is(serr, fs.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("could not delete the %s snapshot %s (%s): remove it with your administrator's rights", ref.Kind, ref.Path, firstLine(out, err))
}

func firstLine(out []byte, err error) string {
	if s, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); s != "" {
		return s
	}
	return err.Error()
}

// Choose returns the native backend that covers every target, or nil when the
// copy backend must be used. store is where snapshots live.
func (h Host) Choose(store string, targets []Target) *Native {
	if len(targets) == 0 {
		return nil
	}
	var paths []string
	for _, t := range targets {
		paths = append(paths, h.existing(resolveParents(t.Path)))
	}
	if n := h.btrfs(store, paths); n != nil {
		return n
	}
	return h.zfs(paths)
}

// Backend names the backend that would cover path: "btrfs", "zfs" or "copy".
func (h Host) Backend(store, path string) string {
	if n := h.Choose(store, []Target{{Path: path}}); n != nil {
		return n.Kind
	}
	return "copy"
}

// existing is the nearest folder at or above p that exists.
func (h Host) existing(p string) string {
	for {
		if _, err := h.Ino(p); err == nil || filepath.Dir(p) == p {
			return p
		}
		p = filepath.Dir(p)
	}
}

// sameFs is the filesystem all paths share when it has type magic.
func (h Host) sameFs(magic int64, paths []string) (Fs, bool) {
	var first Fs
	for i, p := range paths {
		f, err := h.Statfs(p)
		if err != nil || f.Type != magic || (i > 0 && f != first) {
			return Fs{}, false
		}
		first = f
	}
	return first, true
}

func (h Host) btrfs(store string, paths []string) *Native {
	f, ok := h.sameFs(btrfsMagic, paths)
	if !ok {
		return nil
	}
	if sf, err := h.Statfs(h.existing(store)); err != nil || sf != f { // a snapshot must be made on the same filesystem
		return nil
	}
	var root string
	for _, p := range paths {
		r, ok := h.subvolume(p)
		if !ok || (root != "" && r != root) {
			return nil
		}
		root = r
	}
	return &Native{Kind: "btrfs", take: func(ctx context.Context, id, dest string) (NativeRef, error) {
		out, err := h.Run(ctx, "btrfs", "subvolume", "snapshot", "-r", root, dest)
		if err != nil {
			return NativeRef{}, fmt.Errorf("btrfs snapshot: %s", firstLine(out, err))
		}
		return NativeRef{Kind: "btrfs", Root: root, Path: dest}, nil
	}}
}

// subvolume finds the top folder of the btrfs subvolume holding p.
func (h Host) subvolume(p string) (string, bool) {
	for {
		ino, err := h.Ino(p)
		if err != nil {
			return "", false
		}
		if ino == btrfsSubvolIno {
			return p, true
		}
		if filepath.Dir(p) == p {
			return "", false
		}
		p = filepath.Dir(p)
	}
}

func (h Host) zfs(paths []string) *Native {
	if _, ok := h.sameFs(zfsMagic, paths); !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	out, err := h.Run(ctx, "zfs", "list", "-H", "-o", "name,mountpoint", paths[0])
	ds, mount, ok := strings.Cut(strings.TrimSpace(string(out)), "\t")
	if err != nil || !ok || ds == "" || !filepath.IsAbs(mount) {
		return nil // no such tool, or a dataset with no mount (legacy, none)
	}
	for _, p := range paths {
		if rel, err := filepath.Rel(mount, p); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			return nil
		}
	}
	return &Native{Kind: "zfs", take: func(ctx context.Context, id, _ string) (NativeRef, error) {
		name := ds + "@safeshell-" + id
		if out, err := h.Run(ctx, "zfs", "snapshot", name); err != nil {
			return NativeRef{}, fmt.Errorf("zfs snapshot: %s", firstLine(out, err))
		}
		return NativeRef{Kind: "zfs", Root: mount, Path: filepath.Join(mount, ".zfs", "snapshot", "safeshell-"+id), Name: name}, nil
	}}
}
