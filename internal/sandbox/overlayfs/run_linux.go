//go:build linux

package overlayfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/samarthsinh2660/safeshell/internal/sandbox/netwatch"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// Run executes spec.Line inside the sandbox and returns what it did. The real
// filesystem is never written. The binary must be allowed to create user
// namespaces with capabilities (see scripts/install-apparmor.sh).
func Run(ctx context.Context, spec sandbox.Spec) (sandbox.Result, error) {
	spec = spec.Defaults()
	cwd := spec.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	env := spec.Env
	if env == nil {
		env = os.Environ()
	}
	shell := spec.Shell
	if shell == "" {
		shell = "/bin/bash"
	}
	self, err := os.Executable()
	if err != nil {
		return sandbox.Result{}, err
	}
	base := scratchBase()
	sweepStale(base, os.Getuid(), time.Now())
	scratch, err := os.MkdirTemp(base, fmt.Sprintf("ss-sim-%d-", os.Getpid()))
	if err != nil {
		return sandbox.Result{}, err
	}
	defer cleanup(scratch)

	mi, _ := os.ReadFile("/proc/self/mountinfo")
	layers := plan(layerMounts(topDirs(), string(mi)), scratch)
	for _, l := range layers {
		for _, d := range []string{l.Upper, l.Work} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				return sandbox.Result{}, err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(scratch, "merged"), 0o755); err != nil {
		return sandbox.Result{}, err
	}
	req, _ := json.Marshal(request{Spec: spec, Shell: shell, Cwd: cwd, Env: env, Root: scratch, Layers: layers})

	rr, rw, err := os.Pipe()
	if err != nil {
		return sandbox.Result{}, err
	}
	defer rr.Close()

	ctx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()
	stdout, stderr := &capBuffer{max: spec.MaxOutput}, &capBuffer{max: spec.MaxOutput}
	cmd := exec.CommandContext(ctx, self, "sandbox-init")
	cmd.Stdin = bytes.NewReader(req)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	netParent, netChild, err := socketPair()
	if err != nil {
		return sandbox.Result{}, err
	}
	defer netParent.Close()
	cmd.ExtraFiles = []*os.File{rw, netChild}
	cmd.WaitDelay = 2 * time.Second
	cmd.SysProcAttr = initProcAttr()
	start := time.Now()
	if err := cmd.Start(); err != nil {
		rw.Close()
		netChild.Close()
		return sandbox.Result{}, fmt.Errorf("start sandbox: %w", err)
	}
	rw.Close()
	netChild.Close()
	attempts := make(chan []sandbox.NetAttempt, 1)
	go func() { attempts <- superviseNetwork(netParent, cmd.Process.Pid) }()
	_ = cmd.Wait() // init's own status is not used; it reports on fd 3
	res := sandbox.Result{
		Stdout: stdout.String(), Stderr: stderr.String(), Duration: time.Since(start),
		Truncated: stdout.cut || stderr.cut,
	}

	select {
	case res.Net = <-attempts:
	case <-time.After(time.Second):
	}
	var out outcome
	gotOutcome := json.NewDecoder(rr).Decode(&out) == nil
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		res.TimedOut, res.ExitCode = true, -1
	case !gotOutcome:
		return res, fmt.Errorf("sandbox ended without a result: %s", firstLine(res.Stderr))
	case out.Error != "":
		return res, fmt.Errorf("sandbox setup failed: %s%s", out.Error, detail(res.Stderr))
	default:
		res.ExitCode = out.Exit
	}
	res.Warnings = markWritable(out.Warnings)

	if len(out.Layers) > 0 {
		layers = out.Layers
	}
	for _, l := range layers {
		d, err := sandbox.Diff(l.Upper, l.Mount, sandbox.Options{})
		if err != nil {
			return res, fmt.Errorf("read changes of %s: %w", l.Mount, err)
		}
		for _, c := range d.Changes {
			c.Path = filepath.Join(strings.TrimPrefix(l.Mount, "/"), c.Path)
			res.Diff.Changes = append(res.Diff.Changes, c)
		}
		res.Diff.Truncated = res.Diff.Truncated || d.Truncated
	}
	return res, nil
}

// cleanup removes the scratch tree. Overlay work directories are created with
// mode 000, so permissions are opened up first.
func cleanup(root string) {
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if e != nil && e.IsDir() {
			_ = os.Chmod(p, 0o700)
		}
		return nil
	})
	_ = os.RemoveAll(root)
}

// legacyAge is how old a scratch folder without an owner pid in its name must be
// before it counts as left behind (older versions named folders ss-sim-<random>).
const legacyAge = time.Hour

// sweepStale removes scratch folders in base that an earlier run left behind
// (for example after Ctrl-C): ours, and with an owner process that is gone. It
// never follows symlinks and ignores anything not owned by the current user.
func sweepStale(base string, uid int, now time.Time) {
	entries, _ := os.ReadDir(base)
	for _, e := range entries {
		rest, ok := strings.CutPrefix(e.Name(), "ss-sim-")
		if !ok || !e.IsDir() { // a symlink is not a directory here
			continue
		}
		p := filepath.Join(base, e.Name())
		fi, err := os.Lstat(p)
		if err != nil || int(fi.Sys().(*syscall.Stat_t).Uid) != uid {
			continue
		}
		if stale(rest, fi.ModTime(), now) {
			cleanup(p)
		}
	}
}

// stale says whether a folder named ss-sim-<rest> was left behind: its owner
// pid is gone, or, for the old naming without a pid, it is older than legacyAge.
func stale(rest string, modified, now time.Time) bool {
	if pid, _, found := strings.Cut(rest, "-"); found {
		if n, err := strconv.Atoi(pid); err == nil && n > 0 {
			return syscall.Kill(n, 0) == syscall.ESRCH // EPERM means it is running as someone else
		}
	}
	return now.Sub(modified) > legacyAge
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no output (is the AppArmor profile installed? run: safeshell doctor)"
	}
	return s
}

// capBuffer keeps the first max bytes and silently discards the rest, so a
// chatty command can neither fill memory nor block.
type capBuffer struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) <= room {
			c.buf.Write(p)
		} else {
			c.buf.Write(p[:room])
			c.cut = true
		}
	} else if len(p) > 0 {
		c.cut = true
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return c.buf.String() }

// scratchBase picks where the scratch layers live. They must not sit inside
// the filesystem being overlaid or the kernel refuses the mount (EINVAL), so
// the per-user runtime directory (a tmpfs on systemd machines) comes first.
func scratchBase() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			return d
		}
	}
	return os.TempDir()
}

// detail appends whatever the child printed, which helps explain a setup failure.
func detail(stderr string) string {
	if strings.TrimSpace(stderr) == "" {
		return ""
	}
	return "\n" + strings.TrimRight(stderr, "\n")
}

// topDirs lists the real directories directly under "/" that get an overlay.
func topDirs() []string {
	entries, _ := os.ReadDir("/")
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Type()&os.ModeSymlink == 0 && !isSpecialTop(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

func socketPair() (parent, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(fds[0]), "netwatch-parent"), os.NewFile(uintptr(fds[1]), "netwatch-child"), nil
}

// superviseNetwork waits for the child's seccomp descriptor and answers its
// notifications until the sandbox exits. No descriptor means no record.
func superviseNetwork(sock *os.File, pid int) []sandbox.NetAttempt {
	f, err := netwatch.Take(sock, pid)
	if err != nil {
		return nil
	}
	return netwatch.Supervise(f)
}

// initProcAttr is how the sandbox's first process is started: in new
// namespaces, killed with its parent, and in its own session so it has no
// controlling terminal and cannot reach the user's real one.
func initProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS | syscall.CLONE_NEWPID |
			syscall.CLONE_NEWNET | syscall.CLONE_NEWUTS | syscall.CLONE_NEWIPC,
		UidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getuid(), Size: 1}},
		GidMappings: []syscall.SysProcIDMap{{ContainerID: 0, HostID: os.Getgid(), Size: 1}},
		Pdeathsig:   syscall.SIGKILL,
		Setsid:      true,
	}
}

// markWritable records which warning paths the real user can write to. It runs
// here, as that user, not inside the sandbox.
func markWritable(ws []sandbox.Warning) []sandbox.Warning {
	for i := range ws {
		for _, p := range ws[i].Paths {
			if syscall.Access(p, 2 /* W_OK */) == nil {
				ws[i].Writable = append(ws[i].Writable, p)
			}
		}
	}
	return ws
}
