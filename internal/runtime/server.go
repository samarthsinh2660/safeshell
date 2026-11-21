package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// IdleSeconds is how long the server keeps the model in memory without a request.
const IdleSeconds = 600

// Spec says what to run.
type Spec struct {
	Bin   string // llama-server
	Model string // path to the .gguf
	GPU   bool
	// System is a llama-server from the system, not our own build: it keeps its own libraries and working folder.
	System bool
}

type state struct {
	PID   int    `json:"pid"`
	Port  int    `json:"port"`
	Model string `json:"model"`
	Key   string `json:"key"` // the server only answers requests that carry it
	Bin   string `json:"bin"` // the executable, to recognise the process again
}

// Endpoint is how to reach a running server.
type Endpoint struct {
	Port int
	Key  string
}

func (d Dirs) statePath() string { return filepath.Join(d.State, "llama.json") }

func (d Dirs) readState() (state, bool) {
	var s state
	b, err := os.ReadFile(d.statePath())
	if err != nil || json.Unmarshal(b, &s) != nil || s.PID <= 0 {
		return state{}, false
	}
	return s, true
}

// running reports whether s still describes a live engine. A saved PID can
// outlive its process (a reboot, a crash) and be reused by something else, so
// the process must also be running the same executable.
func (s state) running() bool {
	if s.PID <= 0 || syscall.Kill(s.PID, 0) != nil {
		return false
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", s.PID))
	if err != nil {
		return false
	}
	exe, _, _ := strings.Cut(string(b), "\x00")
	return s.Bin != "" && exe == s.Bin
}

// errPortTaken means something other than our engine is listening on the engine's port.
var errPortTaken = errors.New("another program is using the port the local engine needs, so it was not started; run the command again")

// ready reports whether the engine (process pid) is serving on port. The access key is sent only after the socket
// on port is confirmed to belong to pid, so another local user who grabbed the port first never receives it. An
// engine that is not listening yet, or still loading its model (503), is simply not ready.
func ready(ctx context.Context, pid, port int, key string) (bool, error) {
	listening, owned := portOwner("/proc", pid, port)
	if !listening {
		return false, nil
	}
	if !owned {
		return false, errPortTaken
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/v1/models", port), nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, nil
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list) != nil || len(list.Data) == 0 || list.Data[0].ID == "" {
		return false, nil
	}
	return true, nil
}

// Ensure returns the endpoint of a running server for spec, starting one if needed. Starts are serialised with a lock
// so two shells opening at once do not launch two servers.
func (d Dirs) Ensure(ctx context.Context, spec Spec, log io.Writer) (Endpoint, error) {
	if err := os.MkdirAll(d.State, 0o700); err != nil {
		return Endpoint{}, err
	}
	lock, err := os.OpenFile(filepath.Join(d.State, "llama.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return Endpoint{}, err
	}
	defer lock.Close()
	if err := lockWithContext(ctx, lock); err != nil {
		return Endpoint{}, err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)

	if s, ok := d.readState(); ok && s.running() {
		if s.Model == spec.Model {
			ok, err := ready(ctx, s.PID, s.Port, s.Key)
			if ok {
				return Endpoint{s.Port, s.Key}, nil
			}
			if err != nil { // someone else holds the port: this engine is useless and must not be trusted
				d.discard(s.PID)
				return Endpoint{}, err
			}
			if err := ctx.Err(); err != nil { // out of time: that says nothing about the engine
				return Endpoint{}, err
			}
			// Alive with the right model but not answering yet: it is still loading. Only an engine that
			// has taken longer than the start limit is treated as wedged.
			if began := d.startedAt(); time.Since(began) < startLimit {
				return d.waitReady(ctx, s.PID, s.Port, s.Key, began.Add(startLimit), func() bool { return !s.running() })
			}
		}
		_ = syscall.Kill(s.PID, syscall.SIGTERM) // a different model was asked for, or it is wedged
		time.Sleep(300 * time.Millisecond)
	}
	if log != nil {
		fmt.Fprintln(log, "Starting the local AI model...")
	}
	port, err := freePort()
	if err != nil {
		return Endpoint{}, err
	}
	key, err := randomKey()
	if err != nil {
		return Endpoint{}, err
	}
	logf, _ := os.OpenFile(filepath.Join(d.State, "llama.log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	keyFile := filepath.Join(d.State, "llama.key") // not on the command line, where any user could read it with ps
	if err := os.WriteFile(keyFile, []byte(key), 0o600); err != nil {
		return Endpoint{}, err
	}
	args := []string{"-m", spec.Model, "--host", "127.0.0.1", "--port", strconv.Itoa(port),
		"--api-key-file", keyFile, "-c", "4096", "--parallel", "1", "--no-webui", "--sleep-idle-seconds", strconv.Itoa(IdleSeconds)}
	if !spec.GPU {
		args = append(args, "-ngl", "0")
	}
	cmd := exec.Command(spec.Bin, args...)
	if !spec.System {
		cmd.Dir = filepath.Dir(spec.Bin)
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+filepath.Dir(spec.Bin))
	}
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // outlives the shell command that started it
	if err := cmd.Start(); err != nil {
		return Endpoint{}, fmt.Errorf("could not start the local engine: %w", err)
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	b, _ := json.Marshal(state{PID: cmd.Process.Pid, Port: port, Model: spec.Model, Key: key, Bin: spec.Bin})
	_ = os.WriteFile(d.statePath(), b, 0o600)

	return d.waitReady(ctx, cmd.Process.Pid, port, key, time.Now().Add(startLimit), func() bool {
		select {
		case <-exited:
			return true
		default:
			return false
		}
	})
}

// startLimit is how long an engine may take to load its model.
const startLimit = 180 * time.Second

// startedAt is when the engine in the state file was started.
func (d Dirs) startedAt() time.Time {
	fi, err := os.Stat(d.statePath())
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

// waitReady waits until the engine (process pid) on port answers, it stops (gone), end passes or ctx ends.
func (d Dirs) waitReady(ctx context.Context, pid, port int, key string, end time.Time, gone func() bool) (Endpoint, error) {
	for time.Now().Before(end) {
		select {
		case <-ctx.Done():
			return Endpoint{}, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
		if gone() {
			return Endpoint{}, fmt.Errorf("the local engine stopped while starting (see %s)", filepath.Join(d.State, "llama.log"))
		}
		ok, err := ready(ctx, pid, port, key)
		if err != nil {
			d.discard(pid)
			return Endpoint{}, err
		}
		if ok {
			return Endpoint{port, key}, nil
		}
	}
	return Endpoint{}, errors.New("the local engine did not become ready within 3 minutes")
}

// discard stops an engine that cannot be trusted and forgets it, so the next start picks a new port.
func (d Dirs) discard(pid int) {
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = os.Remove(d.statePath())
}

// lockWithContext takes the exclusive lock on f without blocking past ctx.
func lockWithContext(ctx context.Context, f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err != syscall.EWOULDBLOCK {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Stop ends the background server, if one is running.
func (d Dirs) Stop() bool {
	s, ok := d.readState()
	if !ok || !s.running() {
		return false
	}
	_ = syscall.Kill(s.PID, syscall.SIGTERM)
	_ = os.Remove(d.statePath())
	return true
}

// Running reports the background server's state without starting it.
func (d Dirs) Running() (model string, ok bool) {
	s, found := d.readState()
	if !found || !s.running() {
		return "", false
	}
	return s.Model, true
}

func randomKey() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
