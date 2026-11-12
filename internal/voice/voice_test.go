package voice

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// TestMain lets the test binary stand in for a recorder: it writes a WAV file
// to its last argument and waits for SIGINT.
func TestMain(m *testing.M) {
	switch os.Getenv("VOICE_FAKE_RECORDER") {
	case "wav":
		f := os.Args[len(os.Args)-1]
		_ = os.WriteFile(f, append([]byte("RIFF"), make([]byte, 100)...), 0o600)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "empty":
		time.Sleep(time.Minute)
		os.Exit(0)
	case "exit":
		os.Exit(0)
	case "deaf": // writes audio, then ignores SIGINT so only SIGKILL stops it
		signal.Ignore(os.Interrupt)
		_ = os.WriteFile(os.Args[len(os.Args)-1], append([]byte("RIFF"), make([]byte, 100)...), 0o600)
		time.Sleep(time.Minute)
		os.Exit(0)
	case "echo":
		fmt.Println("hello there")
		fmt.Fprintln(os.Stderr, "warning")
		os.Exit(0)
	case "fail":
		os.Exit(3)
	}
	os.Exit(m.Run())
}

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"  list   the files \n here ":        "list the files here",
		"[BLANK_AUDIO]":                      "",
		" [MUSIC] show disk usage (music) ":  "show disk usage",
		"(clears throat) what is on port 80": "what is on port 80",
		"":                                   "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

func lookIn(have ...string) func(string) (string, error) {
	return func(n string) (string, error) {
		for _, h := range have {
			if h == n {
				return "/usr/bin/" + n, nil
			}
		}
		return "", errors.New("not found")
	}
}

func TestPickRecorderOrder(t *testing.T) {
	for _, tc := range []struct {
		have []string
		want string
	}{
		{[]string{"ffmpeg", "arecord", "parec", "pw-record"}, "pw-record"},
		{[]string{"ffmpeg", "arecord", "parec"}, "parec"},
		{[]string{"ffmpeg", "arecord"}, "arecord"},
		{[]string{"ffmpeg"}, "ffmpeg"},
	} {
		r, ok := PickRecorder(lookIn(tc.have...))
		if !ok || r.Name != tc.want || r.Bin != "/usr/bin/"+tc.want {
			t.Errorf("%v: %+v %v", tc.have, r, ok)
		}
	}
	if _, ok := PickRecorder(lookIn()); ok {
		t.Error("nothing installed must report none")
	}
}

func TestToolRecorderArgs(t *testing.T) {
	for name, want := range map[string][]string{
		"pw-record": {"--rate", "16000", "--channels", "1", "--format", "s16", "F"},
		"parec":     {"--rate=16000", "--channels=1", "--format=s16le", "--file-format=wav", "F"},
		"arecord":   {"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "wav", "F"},
		"ffmpeg":    {"-loglevel", "error", "-f", "pulse", "-i", "default", "-ac", "1", "-ar", "16000", "-y", "F"},
	} {
		if got := (ToolRecorder{Name: name}).Args("F"); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestValidWAV(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, b []byte) string {
		p := filepath.Join(dir, name)
		_ = os.WriteFile(p, b, 0o600)
		return p
	}
	if !validWAV(write("ok", append([]byte("RIFF"), make([]byte, 45)...))) {
		t.Error("a RIFF file with audio must be valid")
	}
	for name, b := range map[string][]byte{
		"empty":   nil,
		"header":  append([]byte("RIFF"), make([]byte, 40)...),
		"notwav":  append([]byte("OggS"), make([]byte, 100)...),
		"tooshrt": []byte("RI"),
	} {
		if validWAV(write(name, b)) {
			t.Errorf("%s must be invalid", name)
		}
	}
	if validWAV(filepath.Join(dir, "missing")) {
		t.Error("a missing file must be invalid")
	}
}

func TestRecordStopsOnSignalAndCleansUp(t *testing.T) {
	t.Setenv("VOICE_FAKE_RECORDER", "wav")
	stop := make(chan struct{})
	go func() { time.Sleep(200 * time.Millisecond); close(stop) }()
	r := ToolRecorder{Name: "arecord", Bin: os.Args[0]}
	wav, cleanup, err := r.Record(context.Background(), stop)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(wav); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("recording file: %v %v", fi, err)
	}
	dir := filepath.Dir(wav)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Error("cleanup must remove the whole folder")
	}
}

func TestRecordTimeLimitAndMissingAudio(t *testing.T) {
	t.Setenv("VOICE_FAKE_RECORDER", "empty")
	r := ToolRecorder{Name: "arecord", Bin: os.Args[0], Max: 100 * time.Millisecond}
	_, _, err := r.Record(context.Background(), make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "The recorder (arecord) did not produce audio. Check your microphone with:  arecord -l") {
		t.Errorf("%v", err)
	}
}

func TestRecordCancelRemovesFiles(t *testing.T) {
	t.Setenv("VOICE_FAKE_RECORDER", "wav")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := (ToolRecorder{Name: "arecord", Bin: os.Args[0]}).Record(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

func TestWhisperCLI(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "request.wav")
	var gotName string
	var gotArgs []string
	w := WhisperCLI{Bin: "/w/whisper-cli", Model: "/m/ggml-base.en.bin", Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		_ = os.WriteFile(filepath.Join(dir, "transcript.txt"), []byte(" [BLANK_AUDIO]\n show  the files \n"), 0o600)
		return []byte("ignored"), nil
	}}
	text, err := w.Transcribe(context.Background(), wav)
	want := []string{"-m", "/m/ggml-base.en.bin", "-f", wav, "-l", "en", "-nt", "-np", "-otxt", "-of", filepath.Join(dir, "transcript")}
	if err != nil || text != "show the files" || gotName != "/w/whisper-cli" || !reflect.DeepEqual(gotArgs, want) {
		t.Errorf("%q %v %s %v", text, err, gotName, gotArgs)
	}

	// No output file: fall back to stdout.
	dir2 := t.TempDir()
	w.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte(" hello  there "), nil }
	if text, err := w.Transcribe(context.Background(), filepath.Join(dir2, "r.wav")); err != nil || text != "hello there" {
		t.Errorf("%q %v", text, err)
	}

	// Silence.
	w.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("[BLANK_AUDIO]"), nil }
	if _, err := w.Transcribe(context.Background(), filepath.Join(dir2, "r.wav")); !errors.Is(err, ErrNoSpeech) {
		t.Errorf("%v", err)
	}
	// Engine failure.
	w.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("exit 1") }
	if _, err := w.Transcribe(context.Background(), filepath.Join(dir2, "r.wav")); err == nil || !strings.Contains(err.Error(), "safeshell voice status") {
		t.Errorf("%v", err)
	}
}

func setup(t *testing.T, have ...string) Setup {
	t.Helper()
	return Setup{Dirs: runtime.Dirs{Data: t.TempDir(), State: t.TempDir()}, LookPath: lookIn(have...), Out: &strings.Builder{}}
}

func TestEnsureEngineNeedsBuildTools(t *testing.T) {
	const msg = "Building the speech engine needs cmake and a C++ compiler. On Ubuntu:  sudo apt install build-essential cmake  then run this again."
	for _, have := range [][]string{nil, {"cmake"}, {"g++"}} {
		if _, err := setup(t, have...).EnsureEngine(context.Background()); err == nil || err.Error() != msg {
			t.Errorf("%v: %v", have, err)
		}
	}
}

// redirect sends every request to the test server.
type redirect struct{ to string }

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(r.to)
	req.URL.Scheme, req.URL.Host = u.Scheme, u.Host
	return http.DefaultTransport.RoundTrip(req)
}

func TestEnsureEngineRejectsWrongArchiveBeforeExtracting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not the real archive"))
	}))
	defer srv.Close()
	s := setup(t, "cmake", "g++")
	s.Client = &http.Client{Transport: redirect{srv.URL}}
	s.Run = func(context.Context, string, ...string) ([]byte, error) {
		t.Error("nothing may be built from an archive that failed its checksum")
		return nil, nil
	}
	_, err := s.EnsureEngine(context.Background())
	if err == nil || !strings.Contains(err.Error(), "checksum") || !strings.Contains(err.Error(), "safeshell voice setup") {
		t.Errorf("%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Data, "engine", "whisper-"+whisperVersion+".build", "src")); !os.IsNotExist(err) {
		t.Error("a bad archive must not be extracted")
	}
	if _, err := os.Stat(s.EnginePath()); !os.IsNotExist(err) {
		t.Error("nothing may be installed")
	}
}

func TestEnsureEnginePrefersPATH(t *testing.T) {
	bin, err := setup(t, "whisper-cli").EnsureEngine(context.Background())
	if err != nil || bin != "/usr/bin/whisper-cli" {
		t.Errorf("%q %v", bin, err)
	}
}

func TestEnsureEngineUsesBuiltCopy(t *testing.T) {
	s := setup(t)
	_ = os.MkdirAll(filepath.Dir(s.EnginePath()), 0o755)
	_ = os.WriteFile(s.EnginePath(), []byte("x"), 0o755)
	if bin, err := s.EnsureEngine(context.Background()); err != nil || bin != s.EnginePath() {
		t.Errorf("%q %v", bin, err)
	}
}

func TestBuildRunsCmakeAndCopiesBinary(t *testing.T) {
	s := setup(t)
	dir := t.TempDir()
	src, build := filepath.Join(dir, "src"), filepath.Join(dir, "build")
	var calls [][]string
	s.Run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		if args[0] == "--build" {
			_ = os.MkdirAll(filepath.Join(build, "bin"), 0o755)
			_ = os.WriteFile(filepath.Join(build, "bin", "whisper-cli"), []byte("binary"), 0o644)
		}
		return nil, nil
	}
	if err := s.build(context.Background(), src, build, s.EnginePath()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("%v", calls)
	}
	want0 := []string{"cmake", "-S", src, "-B", build, "-DBUILD_SHARED_LIBS=OFF", "-DWHISPER_BUILD_TESTS=OFF", "-DWHISPER_BUILD_SERVER=OFF", "-DGGML_NATIVE=OFF", "-DCMAKE_BUILD_TYPE=Release"}
	if !reflect.DeepEqual(calls[0], want0) {
		t.Errorf("configure: %v", calls[0])
	}
	if c := calls[1]; !reflect.DeepEqual(c[:5], []string{"cmake", "--build", build, "--target", "whisper-cli"}) || c[5] != "-j" {
		t.Errorf("build: %v", c)
	}
	if fi, err := os.Stat(s.EnginePath()); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("installed binary: %v %v", fi, err)
	}
	s.Run = func(context.Context, string, ...string) ([]byte, error) { return []byte("boom"), errors.New("exit 2") }
	if err := s.build(context.Background(), src, build, s.EnginePath()); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("%v", err)
	}
}

func TestStatus(t *testing.T) {
	s := setup(t)
	if st := s.Status(); st.EngineBin != "" || st.ModelReady || st.Ready() || st.EnginePath != s.EnginePath() {
		t.Errorf("%+v", st)
	}
	_ = os.MkdirAll(filepath.Dir(s.EnginePath()), 0o755)
	_ = os.WriteFile(s.EnginePath(), []byte("x"), 0o755)
	if st := s.Status(); st.EngineBin != s.EnginePath() || st.EngineFromPATH {
		t.Errorf("%+v", st)
	}
	_ = os.MkdirAll(filepath.Dir(s.ModelPath()), 0o755)
	_ = os.WriteFile(s.ModelPath(), []byte("short"), 0o644)
	if s.Status().ModelReady {
		t.Error("a partial model is not ready")
	}
	if err := os.Truncate(s.ModelPath(), modelSize); err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); !st.ModelReady || !st.Ready() {
		t.Errorf("%+v", st)
	}
	s.LookPath = lookIn("whisper-cli")
	if st := s.Status(); !st.EngineFromPATH || st.EngineBin != "/usr/bin/whisper-cli" {
		t.Errorf("%+v", st)
	}
}
