package ipc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenFailsWhenAStaleSocketCannotBeReplaced(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	dir := privateDir(t)
	path := filepath.Join(dir, "s.sock")
	if err := os.WriteFile(path, nil, 0o600); err != nil { // leftover, nobody answers
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	if _, err := Listen(path); err == nil {
		t.Error("a leftover that cannot be removed must be an error")
	}
}

func TestServeReturnsAnAcceptFailure(t *testing.T) {
	l, err := Listen(filepath.Join(privateDir(t), "s.sock"))
	if err != nil {
		t.Fatal(err)
	}
	l.Close() // the listener fails while the context is still live
	if err := (&Server{}).Serve(context.Background(), l); err == nil {
		t.Error("expected the accept error")
	}
}

func TestAResultThatCannotBeEncodedIsAServerError(t *testing.T) {
	path := start(t, &Server{Handlers: map[string]Handler{
		"bad": func(context.Context, json.RawMessage) (any, error) { return make(chan int), nil },
	}})
	err := Call(context.Background(), path, "bad", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "could not encode") {
		t.Errorf("%v", err)
	}
}

func TestCallRejectsParamsThatAreNotJSON(t *testing.T) {
	path := start(t, &Server{})
	if err := Call(context.Background(), path, "ping", make(chan int), nil); err == nil {
		t.Error("params that cannot be encoded must fail before sending")
	}
}

func TestPeerUIDNeedsAUnixConnection(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if _, err := peerUID(a); err == nil {
		t.Error("a pipe has no peer credentials")
	}
}
