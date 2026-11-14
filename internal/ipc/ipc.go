// Package ipc is the tiny JSON-RPC 2.0 protocol the shell hook uses to talk to
// the SafeShell daemon over a Unix socket. One JSON object per line. The socket
// is private to the user (mode 0600, and each connection's user id is checked),
// every call has a deadline, and unknown fields are ignored so versions can differ.
package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Protocol is the version reported by ping.
const Protocol = 1

// Standard JSON-RPC error codes, plus one for a handler that failed.
const (
	CodeParse          = -32700
	CodeInvalidRequest = -32600
	CodeNoMethod       = -32601
	CodeBadParams      = -32602
	CodeServer         = -32000
)

const maxLine = 1 << 20

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return fmt.Sprintf("%s (code %d)", e.Message, e.Code) }

// Handler serves one method. Return an *Error for a specific code.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// Server answers calls on a listener.
type Server struct {
	Handlers map[string]Handler
	Deadline time.Duration // per call; default 2 minutes
	UID      int           // the only user allowed to connect; default: the daemon's own
}

// SocketPath is where the daemon listens: $XDG_RUNTIME_DIR/safeshell.sock. It is
// empty when there is no absolute runtime folder; there is then no daemon (a
// shared folder such as /tmp is never used).
func SocketPath() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(d) {
		return filepath.Join(d, "safeshell.sock")
	}
	return ""
}

// ErrUntrusted means the socket, its folder or the program behind it does not
// belong to the current user, so nothing was sent to it.
var ErrUntrusted = errors.New("the SafeShell daemon socket does not belong to you")

func owner(fi fs.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}

// checkOwner requires a socket owned by uid inside a folder owned by uid that
// no one else can write to.
func checkOwner(sock, dir fs.FileInfo, uid int) error {
	if sock.Mode().Type() != fs.ModeSocket || owner(sock) != uid ||
		!dir.IsDir() || owner(dir) != uid || dir.Mode().Perm()&0o022 != 0 {
		return ErrUntrusted
	}
	return nil
}

func checkPath(path string, uid int) error {
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return err
	}
	sock, err := os.Lstat(path)
	if err != nil {
		return err
	}
	return checkOwner(sock, dir, uid)
}

// checkPeer requires the process on the other end of c to run as uid.
func checkPeer(c net.Conn, uid int) error {
	if peer, err := peerUID(c); err != nil || peer != uid {
		return ErrUntrusted
	}
	return nil
}

// maxSocketPath is the longest socket path in bytes the system accepts. SafeShell
// supports Linux only, where the sun_path field is 108 bytes including the NUL
// terminator, so the longest path is 107 bytes.
const maxSocketPath = 107

// ErrPathTooLong means the socket path is longer than the system allows. The
// message says which folder to shorten.
var ErrPathTooLong = errors.New("the SafeShell socket path is too long")

// checkLength refuses a socket path the system cannot bind, before the bind
// fails with a raw "invalid argument" error.
func checkLength(path string) error {
	if len(path) <= maxSocketPath {
		return nil
	}
	return fmt.Errorf("%w (%d bytes, the limit is %d): %s. Commands are still checked, just without the background daemon. To use the daemon, set XDG_RUNTIME_DIR to a shorter folder that only you can write to (for example: mkdir -m 700 /tmp/ss-$USER)",
		ErrPathTooLong, len(path), maxSocketPath, path)
}

// Listen creates the private socket. A leftover socket from a crashed daemon is
// replaced; one that still answers is an error.
func Listen(path string) (net.Listener, error) {
	if err := checkLength(path); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(path); err == nil {
		if c, err := net.DialTimeout("unix", path, 300*time.Millisecond); err == nil {
			c.Close()
			return nil, errors.New("the SafeShell daemon is already running")
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	}
	old := syscall.Umask(0o177) // the socket file is created 0600 from the start, never briefly open
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	return l, err
}

// Serve accepts connections until ctx ends or the listener closes.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	go func() { <-ctx.Done(); l.Close() }()
	uid := s.UID
	if uid == 0 {
		uid = os.Getuid()
	}
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		c, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if peer, err := peerUID(c); err != nil || peer != uid {
			c.Close() // another user (or one we cannot identify) gets nothing
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.conn(ctx, c) }()
	}
}

func (s *Server) conn(ctx context.Context, c net.Conn) {
	defer c.Close()
	r := bufio.NewReaderSize(c, 64<<10)
	enc := json.NewEncoder(c)
	for {
		line, err := readLine(r)
		if err != nil {
			if errors.Is(err, errTooLong) {
				_ = enc.Encode(failure(nil, CodeInvalidRequest, "request too large"))
			}
			return
		}
		var req request
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(failure(nil, CodeParse, "parse error"))
			continue
		}
		resp := s.dispatch(ctx, req)
		if len(req.ID) == 0 { // a notification gets no answer
			continue
		}
		if err := enc.Encode(resp); err != nil {
			return
		}
	}
}

var errTooLong = errors.New("line too long")

func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, err
		}
		out = append(out, chunk...)
		if len(out) > maxLine {
			return nil, errTooLong
		}
		if !isPrefix {
			return out, nil
		}
	}
}

func (s *Server) dispatch(ctx context.Context, req request) response {
	if req.JSONRPC != "2.0" || req.Method == "" {
		return failure(req.ID, CodeInvalidRequest, "not a JSON-RPC 2.0 request")
	}
	h, ok := s.Handlers[req.Method]
	if !ok {
		return failure(req.ID, CodeNoMethod, "unknown method "+req.Method)
	}
	d := s.Deadline
	if d == 0 {
		d = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	type outcome struct {
		v   any
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil { // a bug in one handler must not take the daemon down
				done <- outcome{err: fmt.Errorf("internal error: %v", r)}
			}
		}()
		v, err := h(ctx, req.Params)
		done <- outcome{v, err}
	}()
	select {
	case <-ctx.Done():
		return failure(req.ID, CodeServer, "the call took too long")
	case o := <-done:
		if o.err != nil {
			var e *Error
			if errors.As(o.err, &e) {
				return failure(req.ID, e.Code, e.Message)
			}
			return failure(req.ID, CodeServer, o.err.Error())
		}
		b, err := json.Marshal(o.v)
		if err != nil {
			return failure(req.ID, CodeServer, "could not encode the result")
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: b}
	}
}

func failure(id json.RawMessage, code int, msg string) response {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return response{JSONRPC: "2.0", ID: id, Error: &Error{code, msg}}
}

// Call makes one request on a fresh connection and decodes the result into out (which may be nil).
// It talks only to a socket that belongs to the current user (ErrUntrusted otherwise).
// The peer check on the connection is the security boundary; the path check
// before it is an early warning that avoids connecting to an obvious fake.
func Call(ctx context.Context, path, method string, params, out any) error {
	uid := os.Getuid()
	if err := checkLength(path); err != nil {
		return err
	}
	if err := checkPath(path, uid); err != nil {
		return err
	}
	return call(ctx, uid, path, method, params, out)
}

func call(ctx context.Context, uid int, path, method string, params, out any) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := checkPeer(c, uid); err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(c).Encode(request{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: method, Params: p}); err != nil {
		return err
	}
	var resp response
	if err := json.NewDecoder(io.LimitReader(c, 16<<20)).Decode(&resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(resp.Result, out)
}
