package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestListenInodesReadsOnlyListeningSocketsOnThePort(t *testing.T) {
	for _, c := range []struct {
		file string
		port int
		want []string
	}{
		{"testdata/proc_net_tcp", 0x1F90, []string{"11111", "22222"}}, // the established connection, short and bad lines are skipped
		{"testdata/proc_net_tcp", 0x1F91, []string{"33333"}},
		{"testdata/proc_net_tcp", 0x1F99, nil},
		{"testdata/proc_net_tcp6", 0x1F92, []string{"66666"}},
	} {
		f, err := os.Open(c.file)
		if err != nil {
			t.Fatal(err)
		}
		got := listenInodes(f, c.port)
		f.Close()
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s:%x got %v want %v", c.file, c.port, got, c.want)
		}
	}
}

// fakeProc builds a /proc where pid holds the given socket inodes.
func fakeProc(t *testing.T, pid int, inodes ...string) string {
	t.Helper()
	proc := t.TempDir()
	if err := os.MkdirAll(filepath.Join(proc, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"tcp", "tcp6"} {
		b, err := os.ReadFile("testdata/proc_net_" + n)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(proc, "net", n), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fd := filepath.Join(proc, strconv.Itoa(pid), "fd")
	if err := os.MkdirAll(fd, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, in := range inodes {
		if err := os.Symlink("socket:["+in+"]", filepath.Join(fd, strconv.Itoa(i+3))); err != nil {
			t.Fatal(err)
		}
	}
	return proc
}

func TestPortOwner(t *testing.T) {
	for _, c := range []struct {
		name            string
		inodes          []string
		port            int
		listening, owns bool
	}{
		{"owner holds the only listener", []string{"33333", "1"}, 0x1F91, true, true},
		{"v6 listener", []string{"66666"}, 0x1F92, true, true},
		{"owner holds one of two listeners", []string{"11111"}, 0x1F90, true, false},
		{"another process holds it", []string{"99999"}, 0x1F91, true, false},
		{"nothing listens yet", []string{"33333"}, 0x1F99, false, false},
	} {
		l, o := portOwner(fakeProc(t, 42, c.inodes...), 42, c.port)
		if l != c.listening || o != c.owns {
			t.Errorf("%s: got %v %v", c.name, l, o)
		}
	}
	if l, o := portOwner(t.TempDir(), 42, 0x1F91); l || o {
		t.Error("an unreadable /proc must not look owned")
	}
}

// engineStub serves on 127.0.0.1 and records whether anything sent it an access key.
func engineStub(t *testing.T, handler http.HandlerFunc) (port int, gotAuth func() bool) {
	t.Helper()
	var mu sync.Mutex
	seen := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = seen || r.Header.Get("Authorization") != ""
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ = strconv.Atoi(u.Port())
	return port, func() bool { mu.Lock(); defer mu.Unlock(); return seen }
}

func TestReadyRequiresAModelListFromTheOwnerOfThePort(t *testing.T) {
	reply := func(s string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(s)) }
	}
	for _, c := range []struct {
		name string
		h    http.HandlerFunc
		want bool
	}{
		{"model listed", reply(`{"object":"list","data":[{"id":"m"}]}`), true},
		{"health style reply only", reply(`{"status":"ok"}`), false},
		{"empty list", reply(`{"data":[]}`), false},
		{"model without an id", reply(`{"data":[{}]}`), false},
		{"not JSON", reply(`ok`), false},
		{"still loading", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "loading", http.StatusServiceUnavailable) }, false},
	} {
		port, _ := engineStub(t, c.h)
		got, err := ready(context.Background(), os.Getpid(), port, "k")
		if err != nil || got != c.want {
			t.Errorf("%s: got %v %v", c.name, got, err)
		}
	}
}

func TestReadyIsFalseNotAnErrorWhileNothingIsListening(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if ok, err := ready(context.Background(), os.Getpid(), port, "k"); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
}

func TestReadyIsFalseWhenTheOwnerDoesNotAnswer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0") // listens (so it is ours) but never replies
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if ok, err := ready(ctx, os.Getpid(), l.Addr().(*net.TCPAddr).Port, "k"); ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
}

func TestAnImposterOnTheEnginePortGetsNoKeyAndIsReported(t *testing.T) {
	// This process runs the imposter; the engine we expect is another process (a sleeping child).
	victim := exec.Command("sleep", "30")
	if err := victim.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	defer func() { _ = victim.Process.Kill(); _ = victim.Wait() }()
	port, gotAuth := engineStub(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"status":"ok","object":"list","data":[{"id":"m"}]}`)) // answers anything, with or without a key
	})
	d := Dirs{State: t.TempDir()}
	_, err := d.waitReady(context.Background(), victim.Process.Pid, port, "secret", time.Now().Add(5*time.Second), func() bool { return false })
	if !errors.Is(err, errPortTaken) {
		t.Fatalf("want errPortTaken, got %v", err)
	}
	if gotAuth() {
		t.Error("the access key was sent to a program that does not own the engine's port")
	}
}

func TestEnsureRejectsAnImposterOnAReusedEngineAndStopsIt(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	if _, err := d.Ensure(context.Background(), spec, nil); err != nil {
		t.Fatal(err)
	}
	s, _ := d.readState()
	// The state file now points at a port held by another process (this test's stub), as if it had been taken.
	port, gotAuth := engineStub(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"data":[{"id":"m"}]}`)) })
	taken := s
	taken.Port = port
	b, _ := json.Marshal(taken)
	if err := os.WriteFile(d.statePath(), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Ensure(context.Background(), spec, nil); !errors.Is(err, errPortTaken) {
		t.Fatalf("got %v", err)
	}
	if gotAuth() {
		t.Error("the key reached the imposter")
	}
	if _, ok := d.readState(); ok {
		t.Error("the untrusted engine is still recorded")
	}
	for i := 0; i < 50 && syscall.Kill(s.PID, 0) == nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	if syscall.Kill(s.PID, 0) == nil {
		t.Error("the untrusted engine was not stopped")
	}
	// The next attempt starts a fresh engine.
	if ep, err := d.Ensure(context.Background(), spec, nil); err != nil || ep.Port == port {
		t.Errorf("the next attempt must start cleanly: %v %v", ep, err)
	}
}
