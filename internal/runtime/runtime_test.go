package runtime

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/models"
)

// TestMain lets the test binary stand in for llama-server: when started with
// SS_FAKE_LLAMA it serves /health on the --port it is given.
func TestMain(m *testing.M) {
	if os.Getenv("SS_FAKE_LLAMA") != "" {
		for i, a := range os.Args {
			if a == "--port" {
				port, _ := strconv.Atoi(os.Args[i+1])
				if os.Getenv("SS_FAKE_LLAMA") == "crash" {
					os.Exit(3)
				}
				mux := http.NewServeMux()
				// SS_FAKE_LOADING_MS makes it answer 503 "loading" for that long first, like a big model loading.
				loaded := time.Now()
				if ms, _ := strconv.Atoi(os.Getenv("SS_FAKE_LOADING_MS")); ms > 0 {
					loaded = loaded.Add(time.Duration(ms) * time.Millisecond)
				}
				// Like llama-server it answers the model list only to the key it was started with.
				keyFile := ""
				for j, b := range os.Args {
					if b == "--api-key-file" {
						keyFile = os.Args[j+1]
					}
				}
				mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
					want, _ := os.ReadFile(keyFile)
					if r.Header.Get("Authorization") != "Bearer "+string(want) {
						http.Error(w, "no", http.StatusUnauthorized)
						return
					}
					if time.Now().Before(loaded) {
						http.Error(w, `{"error":"loading model"}`, http.StatusServiceUnavailable)
						return
					}
					w.Write([]byte(`{"object":"list","data":[{"id":"m"}]}`))
				})
				mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
					if time.Now().Before(loaded) {
						http.Error(w, `{"error":"loading model"}`, http.StatusServiceUnavailable)
						return
					}
					w.Write([]byte(`{"status":"ok"}`))
				})
				_ = http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), mux)
			}
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i * 7)
	}
	return b
}

func TestDownloadVerifiesAndResumes(t *testing.T) {
	data := payload(200_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "f", time.Time{}, strings.NewReader(string(data)))
	}))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "d", "f.bin")
	// A previous interrupted run left the first 50 kB.
	os.MkdirAll(filepath.Dir(dest), 0o755)
	os.WriteFile(dest+".part", data[:50_000], 0o644)
	var out strings.Builder
	if err := Download(context.Background(), nil, srv.URL, dest, sum(data), int64(len(data)), &out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(data) {
		t.Error("content differs")
	}
	if _, err := os.Stat(dest + ".part"); err == nil {
		t.Error("partial file left behind")
	}
	if !strings.Contains(out.String(), "100%") {
		t.Errorf("progress: %q", out.String())
	}
	// A second call finds the verified file and does not touch the network.
	srv.Close()
	if err := Download(context.Background(), nil, srv.URL, dest, sum(data), int64(len(data)), nil); err != nil {
		t.Errorf("already downloaded: %v", err)
	}
}

func TestDownloadRejectsAndDeletesCorruptFiles(t *testing.T) {
	data := payload(10_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f.bin")
	err := Download(context.Background(), nil, srv.URL, dest, sum([]byte("something else")), int64(len(data)), nil)
	if err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("err %v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, e := os.Stat(p); e == nil {
			t.Errorf("%s was kept", p)
		}
	}
}

func TestDownloadStartsOverWhenTheServerIgnoresRanges(t *testing.T) {
	data := payload(20_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) })) // always 200
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f.bin")
	os.WriteFile(dest+".part", data[:5_000], 0o644)
	if err := Download(context.Background(), nil, srv.URL, dest, sum(data), int64(len(data)), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadRejectsMoreDataThanDeclared(t *testing.T) {
	data := payload(20_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	defer srv.Close()
	dest := filepath.Join(t.TempDir(), "f.bin")
	err := Download(context.Background(), nil, srv.URL, dest, sum(data), 10_000, nil)
	if err == nil || !strings.Contains(err.Error(), "more data than expected") {
		t.Fatalf("err %v", err)
	}
	for _, p := range []string{dest, dest + ".part"} {
		if _, e := os.Stat(p); e == nil {
			t.Errorf("%s was kept", p)
		}
	}
}

func TestDownloadHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := Download(context.Background(), nil, srv.URL, filepath.Join(t.TempDir(), "x"), "00", 10, nil); err == nil {
		t.Error("404 accepted")
	}
}

type entry struct {
	name, link, body string
	typ              byte
	mode             int64
}

func archive(t *testing.T, entries ...entry) string {
	p := filepath.Join(t.TempDir(), "a.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tw.WriteHeader(&tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.typ, Mode: e.mode, Size: int64(len(e.body))})
		tw.Write([]byte(e.body))
	}
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestExtractStripsTopFolderKeepsModesAndLinks(t *testing.T) {
	a := archive(t,
		entry{name: "llama-b1/", typ: tar.TypeDir, mode: 0o755},
		entry{name: "llama-b1/llama-server", typ: tar.TypeReg, mode: 0o755, body: "bin"},
		entry{name: "llama-b1/libx.so.0", typ: tar.TypeReg, mode: 0o644, body: "lib"},
		entry{name: "llama-b1/libx.so", typ: tar.TypeSymlink, link: "libx.so.0"},
	)
	dest := filepath.Join(t.TempDir(), "e")
	if err := ExtractTarGz(a, dest, nil); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Join(dest, "llama-server")); err != nil || fi.Mode()&0o111 == 0 {
		t.Errorf("server not executable: %v %v", fi, err)
	}
	if l, _ := os.Readlink(filepath.Join(dest, "libx.so")); l != "libx.so.0" {
		t.Errorf("link %q", l)
	}
}

func TestExtractRefusesEscapes(t *testing.T) {
	for name, e := range map[string]entry{
		"dotdot":        {name: "top/../../evil", typ: tar.TypeReg, mode: 0o644, body: "x"},
		"abs symlink":   {name: "top/l", typ: tar.TypeSymlink, link: "/etc/passwd"},
		"up symlink":    {name: "top/l", typ: tar.TypeSymlink, link: "../../outside"},
		"nested dotdot": {name: "top/a/../../../evil", typ: tar.TypeReg, mode: 0o644, body: "x"},
	} {
		dest := filepath.Join(t.TempDir(), "e")
		if err := ExtractTarGz(archive(t, e), dest, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestExtractRefusesWritingThroughChainedSymlinks(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "x", "y", "install")
	a := archive(t,
		entry{name: "top/a", typ: tar.TypeSymlink, link: "."},
		entry{name: "top/a/a/L", typ: tar.TypeSymlink, link: "../.."},
		entry{name: "top/L/escaped.txt", typ: tar.TypeReg, mode: 0o644, body: "x"},
	)
	if err := ExtractTarGz(a, dest, nil); err == nil {
		t.Error("a write through a symlink that leaves the folder was accepted")
	}
	if _, err := os.Stat(filepath.Join(parent, "x", "escaped.txt")); err == nil {
		t.Error("a file was written outside the install folder")
	}
}

func TestExtractRefusesMakingFoldersThroughASymlink(t *testing.T) {
	parent := t.TempDir()
	dest := filepath.Join(parent, "install")
	a := archive(t,
		entry{name: "top/out", typ: tar.TypeSymlink, link: "."},
		entry{name: "top/out/L", typ: tar.TypeSymlink, link: ".."},
		entry{name: "top/L/dir/", typ: tar.TypeDir, mode: 0o755},
	)
	if err := ExtractTarGz(a, dest, nil); err == nil {
		t.Error("a folder made through a symlink that leaves the folder was accepted")
	}
	if _, err := os.Stat(filepath.Join(parent, "dir")); err == nil {
		t.Error("a folder was made outside the install folder")
	}
}

func TestExtractReportsAnInstallFolderItCannotOpen(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can open any folder")
	}
	dest := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dest, 0o000); err != nil {
		t.Fatal(err)
	}
	if err := ExtractTarGz(archive(t, entry{name: "top/f", typ: tar.TypeReg, mode: 0o644}), dest, nil); err == nil {
		t.Error("a folder that cannot be opened was accepted")
	}
}

func variants(bs []Build) string {
	var v []string
	for _, b := range bs {
		v = append(v, b.Variant)
	}
	return strings.Join(v, ",")
}

func machine(t *testing.T, driverText string, vulkan bool) {
	oe, or := exists, readFile
	t.Cleanup(func() { exists, readFile = oe, or })
	exists = func(p string) bool { return vulkan && strings.HasSuffix(p, "libvulkan.so.1") }
	readFile = func(string) string { return driverText }
}

const nv595 = "NVRM version: NVIDIA UNIX Open Kernel Module for x86_64  595.99.02  Release Build"
const nv575 = "NVRM version: NVIDIA UNIX x86_64 Kernel Module  575.57.08  Thu Jul 10"
const nv535 = "NVRM version: NVIDIA UNIX x86_64 Kernel Module  535.183.01  Sun May 12"

func TestCandidatesPreferTheBestEngineAndAlwaysEndWithCPU(t *testing.T) {
	for _, tc := range []struct {
		name, driver, arch, gpu string
		vulkan                  bool
		want                    string
	}{
		{"new nvidia driver", nv595, "amd64", "auto", true, "cuda13,vulkan,cpu"},
		{"older nvidia driver", nv575, "amd64", "auto", true, "cuda12,vulkan,cpu"},
		{"driver too old for cuda", nv535, "amd64", "auto", true, "vulkan,cpu"},
		{"no nvidia, vulkan loader", "", "amd64", "auto", true, "vulkan,cpu"},
		{"nothing special", "", "amd64", "auto", false, "cpu"},
		{"forced cpu", nv595, "amd64", "cpu", true, "cpu"},
		{"old setting off", nv595, "amd64", "off", true, "cpu"},
		{"forced cuda", nv595, "amd64", "cuda", true, "cuda13,cpu"},
		{"forced cuda without nvidia falls to cpu", "", "amd64", "cuda", true, "cpu"},
		{"forced vulkan even without a loader", "", "amd64", "vulkan", false, "vulkan,cpu"},
		{"arm64 has no cuda build here", nv595, "arm64", "auto", true, "vulkan,cpu"},
		{"unknown arch", nv595, "riscv64", "auto", true, ""},
	} {
		machine(t, tc.driver, tc.vulkan)
		if got := variants(Candidates(tc.arch, tc.gpu)); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestParseNvidiaDriver(t *testing.T) {
	for in, want := range map[string]int{nv595: 595, nv575: 575, nv535: 535, "": 0, "garbage": 0} {
		if got := ParseNvidiaDriver(in); got != want {
			t.Errorf("%q: %d", in, got)
		}
	}
}

func TestEveryBuildIsPinnedAndComplete(t *testing.T) {
	for k, b := range builds {
		for _, as := range append([]Asset{b.Main}, b.Extra...) {
			if len(as.SHA) != 64 || as.Size < 1<<20 || !strings.HasPrefix(as.URL(), "https://github.com/ggml-org/llama.cpp/releases/download/"+Tag+"/") {
				t.Errorf("%s: bad asset %+v", k, as)
			}
		}
		if strings.HasPrefix(b.Variant, "cuda") && len(b.Extra) == 0 {
			t.Errorf("%s needs the CUDA runtime libraries", k)
		}
	}
}

func TestInstallEngineAndModelEndToEndWithFakeServers(t *testing.T) {
	arch := archive(t, entry{name: "t/llama-server", typ: tar.TypeReg, mode: 0o755, body: "bin"})
	ab, _ := os.ReadFile(arch)
	model := payload(30_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
			w.Write(ab)
		} else {
			w.Write(model)
		}
	}))
	defer srv.Close()
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	b := Build{Variant: "cpu", Main: Asset{Name: "x.tar.gz", Size: int64(len(ab)), SHA: sum(ab)}}
	// Point the download at the fake server by overriding URL via a client transport.
	client := &http.Client{Transport: rewrite{srv.URL}}
	if err := d.InstallEngine(context.Background(), client, b, &strings.Builder{}); err != nil || !d.EngineInstalled(b) {
		t.Fatalf("engine: %v", err)
	}
	m := models.Model{Title: "T", File: "m.gguf", URL: "https://example.invalid/m.gguf", Size: int64(len(model)), SHA: sum(model)}
	if d.ModelInstalled(m) {
		t.Fatal("model reported installed too early")
	}
	if err := d.InstallModel(context.Background(), client, m, &strings.Builder{}); err != nil || !d.ModelInstalled(m) {
		t.Fatalf("model: %v", err)
	}
}

type rewrite struct{ to string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL.Path
	req2, _ := http.NewRequest(req.Method, r.to+u, nil)
	req2.Header = req.Header
	return http.DefaultTransport.RoundTrip(req2)
}

func TestEnsureStartsOnceReusesAndRestartsForAnotherModel(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	p1, err := d.Ensure(context.Background(), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p1.Key) < 32 {
		t.Errorf("the server needs a real access key, got %q", p1.Key)
	}
	p2, err := d.Ensure(context.Background(), spec, nil)
	if err != nil || p2 != p1 {
		t.Errorf("a healthy server must be reused with the same key: %v %v %v", p1, p2, err)
	}
	if m, ok := d.Running(); !ok || m != "/m/a.gguf" {
		t.Errorf("running: %q %v", m, ok)
	}
	spec.Model = "/m/b.gguf"
	p3, err := d.Ensure(context.Background(), spec, nil)
	if err != nil || p3.Port == p1.Port || p3.Key == p1.Key {
		t.Errorf("another model needs a new server: %v %v %v", p1, p3, err)
	}
	if !d.Stop() {
		t.Error("stop")
	}
	if _, ok := d.Running(); ok {
		t.Error("still running after stop")
	}
}

// holdLock takes the start lock the way another SafeShell process would.
func holdLock(t *testing.T, d Dirs) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(d.State, "llama.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
}

func TestEnsureStopsWaitingForTheLockWhenItsTimeIsUpAndLeavesTheEngineAlone(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	if _, err := d.Ensure(context.Background(), spec, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := d.readState()
	holdLock(t, d)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := d.Ensure(ctx, spec, nil); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("want the deadline error, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Ensure kept waiting for the lock past its deadline")
	}
	if after, _ := d.readState(); after != before || !after.running() {
		t.Error("the engine another caller started was killed or replaced")
	}
}

func TestEnsureReturnsTheDeadlineErrorInsteadOfKillingAnEngineItCouldNotCheck(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	if _, err := d.Ensure(context.Background(), spec, nil); err != nil {
		t.Fatal(err)
	}
	before, _ := d.readState()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // time ran out while it waited for the lock
	if _, err := d.Ensure(ctx, spec, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("want the context error, got %v", err)
	}
	if after, _ := d.readState(); after != before || !after.running() {
		t.Error("a healthy engine was killed because the caller ran out of time")
	}
}

func TestEnsureWaitsForAnEngineThatIsStillLoadingInsteadOfRestartingIt(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	t.Setenv("SS_FAKE_LOADING_MS", "1500")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}

	short, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := d.Ensure(short, spec, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("the first call should run out of time while the model loads: %v", err)
	}
	first, _ := d.readState()
	ep, err := d.Ensure(context.Background(), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second, _ := d.readState(); second != first || ep.Port != first.Port {
		t.Errorf("the loading engine was restarted: %+v then %+v", first, second)
	}
}

func TestEnsureReportsAnEngineThatStopsWhileItIsWaitedFor(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	t.Setenv("SS_FAKE_LOADING_MS", "60000")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	short, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = d.Ensure(short, spec, nil)
	s, _ := d.readState()
	go func() { time.Sleep(500 * time.Millisecond); _ = syscall.Kill(s.PID, syscall.SIGKILL) }()
	if _, err := d.Ensure(context.Background(), spec, nil); err == nil || !strings.Contains(err.Error(), "stopped while starting") {
		t.Errorf("%v", err)
	}
}

func TestEnsureRestartsAnEngineThatHasBeenLoadingForTooLong(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	t.Setenv("SS_FAKE_LOADING_MS", "60000")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	spec := Spec{Bin: os.Args[0], Model: "/m/a.gguf"}
	short, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, _ = d.Ensure(short, spec, nil)
	stuck, _ := d.readState()
	old := time.Now().Add(-startLimit - time.Minute)
	if err := os.Chtimes(d.statePath(), old, old); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SS_FAKE_LOADING_MS", "0")
	ep, err := d.Ensure(context.Background(), spec, nil)
	if err != nil {
		t.Fatal(err)
	}
	if now, _ := d.readState(); now.PID == stuck.PID || ep.Port != now.Port {
		t.Error("a wedged engine was not replaced")
	}
}

func TestEnsureReportsAnEngineThatCrashes(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "crash")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	_, err := d.Ensure(context.Background(), Spec{Bin: os.Args[0], Model: "/m/a.gguf"}, nil)
	if err == nil || !strings.Contains(err.Error(), "llama.log") {
		t.Errorf("%v", err)
	}
	if _, err := d.Ensure(context.Background(), Spec{Bin: "/nonexistent/llama-server"}, nil); err == nil {
		t.Error("missing binary")
	}
}

func TestAStaleStateFileNeverKillsAnUnrelatedProcess(t *testing.T) {
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	victim := exec.Command("sleep", "30")
	if err := victim.Start(); err != nil {
		t.Skip("no sleep:", err)
	}
	defer func() { _ = victim.Process.Kill(); _ = victim.Wait() }()
	// The state file names the victim's PID as if it were our engine (a reused PID after a reboot).
	b, _ := json.Marshal(state{PID: victim.Process.Pid, Port: 1, Model: "/m/a.gguf", Bin: "/home/me/engine/llama-server"})
	os.WriteFile(d.statePath(), b, 0o600)
	if _, ok := d.Running(); ok {
		t.Error("an unrelated process was taken for the engine")
	}
	if d.Stop() {
		t.Error("Stop claimed to stop a process that is not ours")
	}
	if victim.Process.Signal(syscall.Signal(0)) != nil {
		t.Error("the unrelated process was killed")
	}
}

func TestEnsureGivesUpWhenTheEngineNeverAnswers(t *testing.T) {
	d := Dirs{State: t.TempDir()}
	_, err := d.waitReady(context.Background(), os.Getpid(), 1, "", time.Now(), func() bool { return false })
	if err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Errorf("%v", err)
	}
}

func TestAStateFileThatCannotBeReadHasNoStartTime(t *testing.T) {
	if !(Dirs{State: t.TempDir()}).startedAt().IsZero() {
		t.Error("no state file, no start time")
	}
}

func TestTheAccessKeyIsNotOnTheCommandLine(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	ep, err := d.Ensure(context.Background(), Spec{Bin: os.Args[0], Model: "/m/a.gguf"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := d.readState()
	cmdline, _ := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", s.PID))
	if strings.Contains(string(cmdline), ep.Key) {
		t.Error("the key is visible to every user through ps")
	}
	kf := filepath.Join(d.State, "llama.key")
	if st, err := os.Stat(kf); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v %v", st, err)
	}
	if b, _ := os.ReadFile(kf); string(b) != ep.Key {
		t.Error("key file does not hold the key")
	}
}

func TestBuildSizeAndGPU(t *testing.T) {
	b := Build{Variant: "cuda12", Main: Asset{Size: 10}, Extra: []Asset{{Size: 3}, {Size: 2}}}
	if b.Size() != 15 || !b.GPU() {
		t.Errorf("%d %v", b.Size(), b.GPU())
	}
	if (Build{Variant: "cpu", Main: Asset{Size: 7}}).GPU() || (Build{Variant: "cpu", Main: Asset{Size: 7}}).Size() != 7 {
		t.Error("the cpu build uses no graphics card")
	}
}

func TestDefaultDirsFollowXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	t.Setenv("XDG_STATE_HOME", "/state")
	if d := DefaultDirs(); d.Data != "/data/safeshell" || d.State != "/state/safeshell" {
		t.Errorf("%+v", d)
	}
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/alice")
	if d := DefaultDirs(); d.Data != "/home/alice/.local/share/safeshell" || d.State != "/home/alice/.local/state/safeshell" {
		t.Errorf("%+v", d)
	}
}

// install puts a runnable engine of build b under d.
func install(t *testing.T, d Dirs, b Build) {
	t.Helper()
	bin := d.ServerBin(b)
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInstalledListsEveryInstalledCandidateBestFirst(t *testing.T) {
	machine(t, "", true) // Vulkan present, no NVIDIA driver
	d := Dirs{Data: t.TempDir()}
	if got := d.Installed("amd64", "auto"); len(got) != 0 {
		t.Fatalf("nothing installed yet: %v", got)
	}
	cands := Candidates("amd64", "auto")
	if variants(cands) != "vulkan,cpu" {
		t.Fatalf("candidates %s", variants(cands))
	}
	install(t, d, cands[1])
	if got := variants(d.Installed("amd64", "auto")); got != "cpu" {
		t.Errorf("only the cpu build is installed: %s", got)
	}
	install(t, d, cands[0])
	if got := variants(d.Installed("amd64", "auto")); got != "vulkan,cpu" {
		t.Errorf("both are installed, best first: %s", got)
	}
}

func TestInstallPreferredDownloadsNothingWhenEnginesArePresent(t *testing.T) {
	machine(t, "", true)
	d := Dirs{Data: t.TempDir()}
	for _, b := range Candidates("amd64", "auto") {
		install(t, d, b)
	}
	var out strings.Builder
	if err := d.InstallPreferred(context.Background(), nil, "amd64", "auto", &out); err != nil || out.Len() != 0 {
		t.Errorf("%v %q", err, out.String())
	}
	err := d.InstallPreferred(context.Background(), nil, "riscv64", "auto", &out)
	if err == nil || !strings.Contains(err.Error(), "riscv64") {
		t.Errorf("an architecture with no engine: %v", err)
	}
}

// fakeTTY is a writer that says it is a terminal.
type fakeTTY struct{ bytes.Buffer }

func (*fakeTTY) Terminal() bool { return true }

func TestProgressPlainLinesAndTerminal(t *testing.T) {
	var plain bytes.Buffer
	p := newProgress(&plain, 100, 0, true)
	for i := 0; i < 100; i++ {
		_, _ = p.Write([]byte{0})
	}
	p.Finish()
	if got := strings.Count(plain.String(), "\n"); got != 4 || strings.Contains(plain.String(), "\r") {
		t.Errorf("want 4 plain lines and no CR: %q", plain.String())
	}
	var tty fakeTTY
	q := newProgress(&tty, 100, 0, false)
	_, _ = q.Write([]byte{0})
	q.Finish()
	if !strings.Contains(tty.String(), "\r") {
		t.Errorf("a terminal rewrites one line: %q", tty.String())
	}
	r := NewPercent(&plain)
	r.Update(100)
	if !strings.HasSuffix(plain.String(), "100%\n") {
		t.Errorf("%q", plain.String())
	}
	// Nothing to write to: no panic.
	n := newProgress(nil, 10, 0, true)
	n.Update(5)
	n.Finish()
}

func TestProgressTimeLeft(t *testing.T) {
	var buf bytes.Buffer
	p := newProgress(&buf, 1000, 0, true)
	p.done = 100
	if p.timeLeft() != "" {
		t.Error("no estimate before 3 seconds of data")
	}
	p.start = time.Now().Add(-10 * time.Second) // 10 B/s, 900 B to go
	if got := p.timeLeft(); !strings.Contains(got, "about 2 min left") {
		t.Errorf("%q", got)
	}
	p.done = 990 // 1 s left
	if got := p.timeLeft(); !strings.Contains(got, "1 s left") {
		t.Errorf("%q", got)
	}
	p.print()
	p.start = time.Now().Add(time.Hour) // a clock that has not started: no speed
	if p.speed() != 0 {
		t.Error("no speed before the start")
	}
	if !strings.Contains(buf.String(), "MB/s  1 s left") {
		t.Error("the line shows time left next to the speed")
	}
}

func TestIsTerminal(t *testing.T) {
	if isTerminal(&bytes.Buffer{}) || isTerminal(nil) {
		t.Error("a buffer is not a terminal")
	}
	f, _ := os.Open(os.DevNull) // a character device
	defer f.Close()
	if !isTerminal(f) {
		t.Error("a character device counts as a terminal")
	}
	f.Close()
	if isTerminal(f) {
		t.Error("a closed file is not a terminal")
	}
	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()
	if isTerminal(w) {
		t.Error("a pipe is not a terminal")
	}
}

func TestChecksumAndUnpackReportProgress(t *testing.T) {
	data := bytes.Repeat([]byte("x"), 1000)
	path := filepath.Join(t.TempDir(), "f")
	_ = os.WriteFile(path, data, 0o600)
	sum := sha256.Sum256(data)
	var out bytes.Buffer
	if ok, err := fileMatches(path, hex.EncodeToString(sum[:]), &out); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if !strings.Contains(out.String(), "Checking the download...") || !strings.Contains(out.String(), "100%") {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	a := archive(t, entry{name: "top/f", typ: tar.TypeReg, mode: 0o644, body: "hi"})
	if err := ExtractTarGz(a, filepath.Join(t.TempDir(), "d"), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Unpacking...") || !strings.Contains(out.String(), "100%") {
		t.Errorf("%q", out.String())
	}
}

func TestFileSizeOfClosedFile(t *testing.T) {
	f, _ := os.Open(os.DevNull)
	f.Close()
	if fileSize(f) != 0 {
		t.Error("an unreadable file has size 0")
	}
}

func TestChecksumOfSomethingUnreadableIsAnError(t *testing.T) {
	if ok, err := fileMatches(t.TempDir(), "x", nil); ok || err == nil { // a folder opens but cannot be read
		t.Errorf("ok %v err %v", ok, err)
	}
}

func TestAModelOfTheRightSizeButTheWrongContentIsNotInstalledAndNotHashedAgain(t *testing.T) {
	good := payload(30_000)
	m := models.Model{File: "m.gguf", Size: int64(len(good)), SHA: sum(good)}
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	path := d.ModelPath(m.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := payload(30_000)
	bad[100] ^= 1
	if err := os.WriteFile(path, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if d.ModelInstalled(m) {
		t.Fatal("a corrupted file of the right size counted as installed")
	}
	if b, _ := os.ReadFile(markerPath(path)); string(b) != sum(bad) {
		t.Errorf("the marker should hold the checksum the file really has, got %q", b)
	}
	// The file is now a good one, but the marker says otherwise and the file must not be read again to find out.
	if err := os.WriteFile(path, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if d.ModelInstalled(m) {
		t.Error("the damaged file was hashed again")
	}
	// A new verified download overwrites the marker and fixes it.
	if err := MarkVerified(path, m.SHA); err != nil || !d.ModelInstalled(m) {
		t.Errorf("a verified download should recover the model: %v", err)
	}
}

func TestAModelThatCannotBeReadIsNotInstalledAndGetsNoMarker(t *testing.T) {
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	path := d.ModelPath("m.gguf")
	if err := os.MkdirAll(path, 0o755); err != nil { // a folder: it has a size but cannot be read as a file
		t.Fatal(err)
	}
	fi, _ := os.Stat(path)
	if Verified(path, "x", fi.Size()) {
		t.Error("an unreadable model counted as installed")
	}
	if _, err := os.Stat(markerPath(path)); err == nil {
		t.Error("a marker was written for something that could not be checked")
	}
}

func TestInstallModelReportsADownloadThatFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	m := models.Model{Title: "T", File: "m.gguf", URL: "https://example.invalid/m.gguf", Size: 10, SHA: "x"}
	if err := d.InstallModel(context.Background(), &http.Client{Transport: rewrite{srv.URL}}, m, &strings.Builder{}); err == nil {
		t.Error("a failed download was ignored")
	}
}

func TestInstallModelReportsAChecksumItCannotSave(t *testing.T) {
	model := payload(30_000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(model) }))
	defer srv.Close()
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	m := models.Model{Title: "T", File: "m.gguf", URL: "https://example.invalid/m.gguf", Size: int64(len(model)), SHA: sum(model)}
	if err := os.MkdirAll(markerPath(d.ModelPath(m.File)), 0o755); err != nil { // the marker cannot be written
		t.Fatal(err)
	}
	if err := d.InstallModel(context.Background(), &http.Client{Transport: rewrite{srv.URL}}, m, &strings.Builder{}); err == nil {
		t.Error("a marker that could not be saved was ignored")
	}
}

func TestAnOlderInstallWithoutAMarkerIsCheckedOnceAndThenTrusted(t *testing.T) {
	good := payload(30_000)
	m := models.Model{File: "m.gguf", Size: int64(len(good)), SHA: sum(good)}
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	_ = os.MkdirAll(filepath.Dir(d.ModelPath(m.File)), 0o755)
	if err := os.WriteFile(d.ModelPath(m.File), good, 0o644); err != nil {
		t.Fatal(err)
	}
	if !d.ModelInstalled(m) {
		t.Fatal("a good file from an older version should still count")
	}
	if b, _ := os.ReadFile(markerPath(d.ModelPath(m.File))); string(b) != m.SHA {
		t.Error("the marker was not written")
	}
	// With the marker in place the content is no longer read: damage that keeps the size is not looked at again.
	bad := payload(30_000)
	bad[5] ^= 1
	_ = os.WriteFile(d.ModelPath(m.File), bad, 0o644)
	if !d.ModelInstalled(m) {
		t.Error("a marked file should be trusted without hashing it again")
	}
	_ = os.Truncate(d.ModelPath(m.File), 10)
	if d.ModelInstalled(m) {
		t.Error("a file of another size is never installed")
	}
}

// onPath puts an executable script named llama-server on PATH and returns its path.
func onPath(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "llama-server")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return bin
}

func TestSystemServerIsFoundOnPathOrNamesWhatToInstall(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := SystemServer(); err == nil || !strings.Contains(err.Error(), "install llama.cpp") || !strings.Contains(err.Error(), "bundled") {
		t.Errorf("a missing llama-server must say what to install: %v", err)
	}
	want := onPath(t, "#!/bin/sh\n")
	if got, err := SystemServer(); err != nil || got != want {
		t.Errorf("%q %v, want %q", got, err, want)
	}
}

func TestSystemVersionIsTheFirstLineOrEmpty(t *testing.T) {
	bin := onPath(t, "#!/bin/sh\necho 'version: 1234 (abc)' >&2\necho 'built with gcc' >&2\n")
	if got := SystemVersion(context.Background(), bin); got != "version: 1234 (abc)" {
		t.Errorf("%q", got)
	}
	if got := SystemVersion(context.Background(), filepath.Join(t.TempDir(), "none")); got != "" {
		t.Errorf("an engine that cannot run has no version: %q", got)
	}
}

func TestEnsureStartsASystemEngineAndRecognisesItsProcess(t *testing.T) {
	t.Setenv("SS_FAKE_LLAMA", "1")
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	t.Cleanup(func() { d.Stop() })
	ep, err := d.Ensure(context.Background(), Spec{Bin: os.Args[0], Model: "/m/a.gguf", System: true}, nil)
	if err != nil || ep.Port == 0 {
		t.Fatalf("%v %v", ep, err)
	}
	if s, ok := d.readState(); !ok || !s.running() {
		t.Error("the system engine must be recognised by its PID and executable (the port-owner check relies on it)")
	}
}
