//go:build linux

package overlayfs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/netwatch"
)

// Init is the body of `safeshell sandbox-init`. It runs as PID 1 inside the new
// namespaces: it builds the overlay root, pivots into it, runs the command and
// reports how it ended on fd 3. It returns the process exit status.
func Init() int {
	out := os.NewFile(3, "result")
	report := func(o outcome) int {
		_ = json.NewEncoder(out).Encode(o)
		return 0
	}
	var req request
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil {
		return report(outcome{Error: "bad request: " + err.Error()})
	}
	layers, warnings, err := buildRoot(req)
	if err != nil {
		return report(outcome{Error: err.Error()})
	}
	if err := os.Chdir(req.Cwd); err != nil {
		_ = os.Chdir("/")
	}
	_ = syscall.Sethostname([]byte("safeshell"))
	// No new privileges: setuid programs inside cannot gain anything.
	_, _, _ = syscall.Syscall(syscall.SYS_PRCTL, 38 /* PR_SET_NO_NEW_PRIVS */, 1, 0)

	limitResources()
	watchNetwork() // before the command starts, so it and its children are all watched

	shell := req.Shell
	if _, err := os.Stat(shell); err != nil {
		shell = "/bin/sh"
	}
	cmd := exec.Command(shell, "-c", req.Spec.Line)
	cmd.Env = req.Env
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return report(outcome{Warnings: warnings, Layers: layers})
	case errors.As(err, &ee):
		return report(outcome{Exit: ee.ExitCode(), Warnings: warnings, Layers: layers})
	default:
		return report(outcome{Exit: 127, Warnings: warnings, Layers: layers, Error: "could not start " + shell + ": " + err.Error()})
	}
}

// buildRoot mounts the overlays and pseudo filesystems and pivots into the
// result. It returns the overlays that were really mounted and warnings for
// anything that could not be mirrored: such a path is left empty, never
// exposed writable.
func buildRoot(req request) (layers []Layer, warnings []sandbox.Warning, err error) {
	if err := syscall.Mount("", "/", "", syscall.MS_REC|syscall.MS_PRIVATE, ""); err != nil {
		return nil, nil, fmt.Errorf("make mounts private: %w", err)
	}
	merged := filepath.Join(req.Root, "merged")
	if err := syscall.Mount("tmpfs", merged, "tmpfs", 0, "mode=755,size=4m"); err != nil {
		return nil, nil, fmt.Errorf("mount new root: %w", err)
	}
	if err := skeleton(merged); err != nil {
		return nil, nil, err
	}
	b := &builder{root: req.Root, merged: merged, static: map[string]bool{}}
	for _, l := range req.Layers {
		b.static[l.Mount] = true
	}
	for _, l := range req.Layers {
		for _, p := range []string{l.Mount, l.Upper, l.Work} {
			if strings.ContainsAny(p, ",:") {
				return nil, nil, fmt.Errorf("path %q contains , or : which overlayfs options cannot carry", p)
			}
		}
		b.overlay(l, 0)
	}
	if err := mountPseudo(merged); err != nil {
		return nil, nil, err
	}
	// pivot_root(".", ".") swaps the root without needing a directory for the old one.
	if err := syscall.Chdir(merged); err != nil {
		return nil, nil, fmt.Errorf("enter merged root: %w", err)
	}
	if err := syscall.PivotRoot(".", "."); err != nil {
		return nil, nil, fmt.Errorf("pivot_root: %w", err)
	}
	if err := syscall.Unmount(".", syscall.MNT_DETACH); err != nil {
		return nil, nil, fmt.Errorf("detach old root: %w", err)
	}
	return b.done, b.summary(), syscall.Chdir("/")
}

const maxDepth = 6

type builder struct {
	root, merged string
	static       map[string]bool // mounts that have their own layer in the plan
	done         []Layer
	warnings     []sandbox.Warning // paths that are missing from the sandbox
	partial      []string          // directories whose loose files are not visible
	dyn          int
}

// summary turns the collected problems into a few readable warnings.
func (b *builder) summary() []sandbox.Warning {
	out := append([]sandbox.Warning(nil), b.warnings...)
	if len(b.partial) > 0 {
		shown := b.partial
		if len(shown) > 4 {
			shown = shown[:4]
		}
		out = append(out, sandbox.Warning{Paths: b.partial, Text: fmt.Sprintf("loose files directly inside %d directories (for example %s) are not visible in the sandbox", len(b.partial), strings.Join(shown, ", "))})
	}
	return out
}

// overlay mounts l. A directory with other mounts beneath it is refused by the
// kernel (EINVAL): those mounts are locked to the sandbox's parent. For such a
// directory the children are overlaid one by one instead, which loses only the
// loose files directly inside it (reported as a warning).
func (b *builder) overlay(l Layer, depth int) {
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s", l.Mount, l.Upper, l.Work)
	err := syscall.Mount("overlay", filepath.Join(b.merged, l.Mount), "overlay", 0, opts)
	switch {
	case err == nil:
		b.done = append(b.done, l)
	case errors.Is(err, syscall.EINVAL) && depth < maxDepth:
		b.descend(l.Mount, depth)
	default:
		b.warnings = append(b.warnings, unavailable(l.Mount, err))
	}
}

func unavailable(path string, err error) sandbox.Warning {
	return sandbox.Warning{Paths: []string{path}, Text: fmt.Sprintf("%s is not available in the sandbox (%v)", path, err)}
}

func (b *builder) descend(mount string, depth int) {
	entries, err := os.ReadDir(mount)
	if err != nil {
		b.warnings = append(b.warnings, unavailable(mount, err))
		return
	}
	loose := 0
	for _, e := range entries {
		dst := filepath.Join(b.merged, mount, e.Name())
		child := filepath.Join(mount, e.Name())
		switch {
		case e.Type()&os.ModeSymlink != 0:
			if to, err := os.Readlink(child); err == nil {
				_ = os.Symlink(to, dst)
			}
		case e.IsDir():
			_ = os.Mkdir(dst, 0o755)
			if b.static[child] {
				continue // mounted from its own entry in the plan
			}
			b.dyn++
			dir := filepath.Join(b.root, "dyn", fmt.Sprint(b.dyn))
			l := Layer{Mount: child, Upper: filepath.Join(dir, "upper"), Work: filepath.Join(dir, "work")}
			if os.MkdirAll(l.Upper, 0o755) == nil && os.MkdirAll(l.Work, 0o755) == nil {
				b.overlay(l, depth+1)
			}
		default:
			loose++
		}
	}
	if loose > 0 {
		b.partial = append(b.partial, mount)
	}
}

// mountPseudo gives the sandbox its own /proc, a minimal /dev and an empty /run.
func mountPseudo(root string) error {
	const noexec = syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC
	if err := syscall.Mount("proc", filepath.Join(root, "proc"), "proc", noexec, ""); err != nil {
		return fmt.Errorf("mount /proc: %w", err)
	}
	_ = syscall.Mount("sysfs", filepath.Join(root, "sys"), "sysfs", noexec|syscall.MS_RDONLY, "") // best effort
	dev := filepath.Join(root, "dev")
	if err := syscall.Mount("tmpfs", dev, "tmpfs", syscall.MS_NOSUID, "mode=755,size=1m"); err != nil {
		return fmt.Errorf("mount /dev: %w", err)
	}
	for _, n := range []string{"null", "zero", "full", "random", "urandom", "tty"} {
		target := filepath.Join(dev, n)
		f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			return fmt.Errorf("create /dev/%s: %w", n, err)
		}
		f.Close()
		if err := syscall.Mount("/dev/"+n, target, "", syscall.MS_BIND, ""); err != nil {
			return fmt.Errorf("bind /dev/%s: %w", n, err)
		}
	}
	for link, to := range map[string]string{"fd": "/proc/self/fd", "stdin": "/proc/self/fd/0", "stdout": "/proc/self/fd/1", "stderr": "/proc/self/fd/2"} {
		_ = os.Symlink(to, filepath.Join(dev, link))
	}
	_ = os.Mkdir(filepath.Join(dev, "shm"), 0o1777)
	_ = syscall.Mount("tmpfs", filepath.Join(dev, "shm"), "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=1777")
	if err := syscall.Mount("tmpfs", filepath.Join(root, "run"), "tmpfs", syscall.MS_NOSUID|syscall.MS_NODEV, "mode=755"); err != nil {
		return fmt.Errorf("mount /run: %w", err)
	}
	return nil
}

// skeleton recreates the top level of "/" in the new root: symlinks (such as
// /bin -> usr/bin) are copied and directories become mount points.
func skeleton(root string) error {
	entries, err := os.ReadDir("/")
	if err != nil {
		return err
	}
	for _, e := range entries {
		dst := filepath.Join(root, e.Name())
		switch {
		case e.Type()&os.ModeSymlink != 0:
			to, err := os.Readlink("/" + e.Name())
			if err != nil {
				return err
			}
			if err := os.Symlink(to, dst); err != nil {
				return err
			}
		case e.IsDir():
			if err := os.Mkdir(dst, 0o755); err != nil {
				return err
			}
		}
	}
	return nil
}

// watchNetwork installs the network filter and hands its descriptor to the
// parent through fd 4. If that is impossible the command still runs: the network
// namespace has no route out either way, only the record of attempts is lost.
func watchNetwork() {
	sock := os.NewFile(4, "netwatch")
	if sock == nil {
		return
	}
	defer sock.Close()
	f, err := netwatch.Install()
	if err != nil {
		return
	}
	defer f.Close()
	_ = netwatch.Offer(sock, f)
}

// limitResources caps what a simulated command can consume. cgroups would be
// stricter but need systemd delegation, which a rootless tool cannot assume.
// Limits are inherited by the command and its children.
func limitResources() {
	for res, max := range map[int]uint64{
		syscall.RLIMIT_FSIZE:  1 << 30, // no single file over 1 GiB: the scratch area lives in RAM
		syscall.RLIMIT_CORE:   0,
		syscall.RLIMIT_NOFILE: 8192,
		6 /* RLIMIT_NPROC */ : 4096,
	} {
		_ = syscall.Setrlimit(res, &syscall.Rlimit{Cur: max, Max: max})
	}
}
