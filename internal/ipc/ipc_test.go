package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// privateDir is a temporary folder only we can write to, whatever the umask.
func privateDir(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	if err := os.Chmod(d, 0o700); err != nil {
		t.Fatal(err)
	}
	return d
}
func start(t *testing.T, s *Server) string {
	t.Helper()
	path := filepath.Join(privateDir(t), "s.sock")
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Serve(ctx, l); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return path
}

func echo() map[string]Handler {
	return map[string]Handler{
		"ping": func(context.Context, json.RawMessage) (any, error) { return map[string]int{"protocol": Protocol}, nil },
		"add": func(_ context.Context, p json.RawMessage) (any, error) {
			var a struct{ A, B int }
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, &Error{CodeBadParams, "need A and B"}
			}
			return a.A + a.B, nil
		},
		"boom": func(context.Context, json.RawMessage) (any, error) { panic("handler bug") },
		"fail": func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("disk on fire") },
		"slow": func(ctx context.Context, _ json.RawMessage) (any, error) { <-ctx.Done(); return nil, ctx.Err() },
	}
}

func TestCallRoundTripAndErrors(t *testing.T) {
	path := start(t, &Server{Handlers: echo(), Deadline: 300 * time.Millisecond})
	ctx := context.Background()
	var sum int
	if err := Call(ctx, path, "add", map[string]int{"A": 2, "B": 3}, &sum); err != nil || sum != 5 {
		t.Fatalf("%d %v", sum, err)
	}
	var ping struct{ Protocol int }
	if err := Call(ctx, path, "ping", nil, &ping); err != nil || ping.Protocol != Protocol {
		t.Errorf("%+v %v", ping, err)
	}
	for method, code := range map[string]int{"nope": CodeNoMethod, "fail": CodeServer, "boom": CodeServer, "slow": CodeServer} {
		err := Call(ctx, path, method, nil, nil)
		var e *Error
		if !errors.As(err, &e) || e.Code != code {
			t.Errorf("%s: %v", method, err)
		}
	}
	var e *Error
	if err := Call(ctx, path, "add", "not an object", nil); !errors.As(err, &e) || e.Code != CodeBadParams {
		t.Errorf("bad params: %v", err)
	}
	// The daemon survived the panicking handler.
	if err := Call(ctx, path, "ping", nil, nil); err != nil {
		t.Errorf("daemon died: %v", err)
	}
}

func raw(t *testing.T, path, send string) []string {
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	c.Write([]byte(send))
	var out []string
	sc := bufio.NewScanner(c)
	for sc.Scan() {
		out = append(out, sc.Text())
		if len(out) >= strings.Count(send, "\n") {
			break
		}
	}
	return out
}

func TestMalformedAndInvalidRequestsGetProperErrors(t *testing.T) {
	path := start(t, &Server{Handlers: echo()})
	got := raw(t, path, "{not json\n"+`{"jsonrpc":"1.0","id":1,"method":"ping"}`+"\n")
	if len(got) != 2 || !strings.Contains(got[0], "-32700") || !strings.Contains(got[1], "-32600") {
		t.Errorf("%q", got)
	}
}

func TestNotificationsGetNoAnswerAndUnknownFieldsAreIgnored(t *testing.T) {
	path := start(t, &Server{Handlers: echo()})
	got := raw(t, path, `{"jsonrpc":"2.0","method":"ping"}`+"\n"+`{"jsonrpc":"2.0","id":7,"method":"ping","from_the_future":true}`+"\n")
	if len(got) != 1 || !strings.Contains(got[0], `"id":7`) || !strings.Contains(got[0], `"protocol"`) {
		t.Errorf("%q", got)
	}
}

func TestOversizedRequestsAreRefused(t *testing.T) {
	path := start(t, &Server{Handlers: echo()})
	c, _ := net.Dial("unix", path)
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	go c.Write([]byte(strings.Repeat("x", 3<<20) + "\n"))
	line, _ := bufio.NewReader(c).ReadString('\n')
	if !strings.Contains(line, "too large") {
		t.Errorf("%q", line)
	}
}

func TestSocketIsPrivateAndAnotherUserIsDropped(t *testing.T) {
	path := start(t, &Server{Handlers: echo(), UID: os.Getuid() + 1}) // pretend only some other user may connect
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("socket mode: %v %v", st, err)
	}
	if err := Call(context.Background(), path, "ping", nil, nil); err == nil {
		t.Error("a connection from the wrong user was served")
	}
}

func TestListenReplacesAStaleSocketButNotALiveOne(t *testing.T) {
	path := start(t, &Server{Handlers: echo()})
	if _, err := Listen(path); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Errorf("a second daemon: %v", err)
	}
	stale := filepath.Join(privateDir(t), "stale.sock")
	l, _ := net.Listen("unix", stale)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close() // leaves the file behind, as a crash would
	if l2, err := Listen(stale); err != nil {
		t.Errorf("stale socket not replaced: %v", err)
	} else {
		l2.Close()
	}
}

func TestSocketPathPrefersTheRuntimeDirectory(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	if SocketPath() != "/run/user/1000/safeshell.sock" {
		t.Error(SocketPath())
	}
}

func TestSocketPathIsEmptyWithoutAnAbsoluteRuntimeDirectory(t *testing.T) {
	for _, v := range []string{"", "relative/run"} {
		t.Setenv("XDG_RUNTIME_DIR", v)
		if SocketPath() != "" {
			t.Errorf("%q: %q", v, SocketPath())
		}
	}
}

type fakeInfo struct {
	fs.FileInfo
	mode fs.FileMode
	sys  any
}

func (f fakeInfo) Mode() fs.FileMode { return f.mode }
func (f fakeInfo) IsDir() bool       { return f.mode.IsDir() }
func (f fakeInfo) Sys() any          { return f.sys }

func TestCheckOwnerRefusesAnythingNotOurs(t *testing.T) {
	me := os.Getuid()
	st := func(uid int) any { return &syscall.Stat_t{Uid: uint32(uid)} }
	sock := fakeInfo{mode: fs.ModeSocket | 0o600, sys: st(me)}
	dir := fakeInfo{mode: fs.ModeDir | 0o700, sys: st(me)}
	for name, c := range map[string]struct {
		sock, dir fs.FileInfo
		ok        bool
	}{
		"ours":              {sock, dir, true},
		"not a socket":      {fakeInfo{mode: 0o600, sys: st(me)}, dir, false},
		"socket of another": {fakeInfo{mode: fs.ModeSocket | 0o600, sys: st(me + 1)}, dir, false},
		"unknown owner":     {fakeInfo{mode: fs.ModeSocket | 0o600}, dir, false},
		"folder of another": {sock, fakeInfo{mode: fs.ModeDir | 0o700, sys: st(me + 1)}, false},
		"folder not a dir":  {sock, fakeInfo{mode: 0o700, sys: st(me)}, false},
		"group writable":    {sock, fakeInfo{mode: fs.ModeDir | 0o770, sys: st(me)}, false},
		"world writable":    {sock, fakeInfo{mode: fs.ModeDir | 0o777, sys: st(me)}, false},
	} {
		if err := checkOwner(c.sock, c.dir, me); (err == nil) != c.ok || (err != nil && !errors.Is(err, ErrUntrusted)) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCallRefusesAPathThatIsNotOurSocket(t *testing.T) {
	d := privateDir(t)
	plain := filepath.Join(d, "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Call(context.Background(), plain, "ping", nil, nil); !errors.Is(err, ErrUntrusted) {
		t.Errorf("plain file: %v", err)
	}
	if err := Call(context.Background(), filepath.Join(d, "none.sock"), "ping", nil, nil); err == nil || errors.Is(err, ErrUntrusted) {
		t.Errorf("missing socket: %v", err)
	}
	if err := Call(context.Background(), filepath.Join(d, "no", "dir.sock"), "ping", nil, nil); err == nil || errors.Is(err, ErrUntrusted) {
		t.Errorf("missing folder: %v", err)
	}
	if err := Call(context.Background(), "", "ping", nil, nil); err == nil {
		t.Error("empty path")
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o777); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(open, "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := Call(context.Background(), l.Addr().String(), "ping", nil, nil); !errors.Is(err, ErrUntrusted) {
		t.Errorf("writable folder: %v", err)
	}
}

func TestCallRefusesAPeerRunningAsAnotherUser(t *testing.T) {
	path := start(t, &Server{Handlers: echo()})
	if err := call(context.Background(), os.Getuid()+1, path, "ping", nil, nil); !errors.Is(err, ErrUntrusted) {
		t.Errorf("peer of another user: %v", err)
	}
	if err := Call(context.Background(), path, "ping", nil, nil); err != nil {
		t.Errorf("our own socket: %v", err)
	}
}

func TestCallReportsASocketNobodyListensOn(t *testing.T) {
	stale := filepath.Join(privateDir(t), "stale.sock")
	l, _ := net.Listen("unix", stale)
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	l.Close()
	if err := Call(context.Background(), stale, "ping", nil, nil); err == nil || errors.Is(err, ErrUntrusted) {
		t.Errorf("stale socket: %v", err)
	}
}

func TestCheckPeerReadsTheRealConnectionOwner(t *testing.T) {
	path := filepath.Join(privateDir(t), "p.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := checkPeer(c, os.Getuid()); err != nil {
		t.Errorf("same user: %v", err)
	}
	if err := checkPeer(c, os.Getuid()+1); !errors.Is(err, ErrUntrusted) {
		t.Errorf("other user: %v", err)
	}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if err := checkPeer(a, os.Getuid()); !errors.Is(err, ErrUntrusted) {
		t.Errorf("not a unix connection: %v", err)
	}
}

func TestCallHonoursItsContext(t *testing.T) {
	path := start(t, &Server{Handlers: echo(), Deadline: 5 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := Call(ctx, path, "slow", nil, nil); err == nil || time.Since(start) > 2*time.Second {
		t.Errorf("%v after %v", err, time.Since(start))
	}
}

func TestLongSocketPathGetsAClearMessage(t *testing.T) {
	long := filepath.Join(privateDir(t), strings.Repeat("r", 120), "safeshell.sock")
	if len(long) <= maxSocketPath {
		t.Fatalf("test path is only %d bytes", len(long))
	}
	if _, err := Listen(long); !errors.Is(err, ErrPathTooLong) || !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("Listen: %v", err)
	}
	// The client must not try to connect: it reports the same clear reason and
	// is not mistaken for an untrusted socket, so the hook falls back quietly.
	err := Call(context.Background(), long, "ping", nil, nil)
	if !errors.Is(err, ErrPathTooLong) || errors.Is(err, ErrUntrusted) {
		t.Errorf("Call: %v", err)
	}
}

func TestSocketPathAtTheLimitIsAccepted(t *testing.T) {
	if err := checkLength(filepath.Join(privateDir(t), "s.sock")); err != nil {
		t.Errorf("short path refused: %v", err)
	}
	exact := strings.Repeat("a", maxSocketPath)
	if err := checkLength(exact); err != nil {
		t.Errorf("path of exactly %d bytes refused: %v", maxSocketPath, err)
	}
	if err := checkLength(exact + "a"); !errors.Is(err, ErrPathTooLong) {
		t.Errorf("one byte over the limit accepted: %v", err)
	}
}
