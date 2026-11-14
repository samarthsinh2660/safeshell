package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
				mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) })
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
	if err := ExtractTarGz(a, dest); err != nil {
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
		if err := ExtractTarGz(archive(t, e), dest); err == nil {
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
	if err := ExtractTarGz(a, dest); err == nil {
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
	if err := ExtractTarGz(a, dest); err == nil {
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
	if err := ExtractTarGz(archive(t, entry{name: "top/f", typ: tar.TypeReg, mode: 0o644}), dest); err == nil {
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
