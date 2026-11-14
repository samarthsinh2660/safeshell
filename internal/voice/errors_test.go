package voice

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArgsAndHintsForEveryTool(t *testing.T) {
	if (ToolRecorder{Name: "nope"}).Args("F") != nil {
		t.Error("an unknown tool has no arguments")
	}
	for name, want := range map[string]string{"pw-record": "wpctl status", "parec": "pactl list short sources", "arecord": "arecord -l", "ffmpeg": "ffmpeg -sources pulse"} {
		if got := (ToolRecorder{Name: name}).checkHint(); got != want {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestRecordFailures(t *testing.T) {
	// The recorder cannot start (and Bin falls back to Name).
	_, _, err := ToolRecorder{Name: "safeshell-no-such-recorder"}.Record(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "cannot start the recorder") {
		t.Errorf("%v", err)
	}
	// The recorder exits by itself without audio.
	t.Setenv("VOICE_FAKE_RECORDER", "exit")
	_, _, err = ToolRecorder{Name: "ffmpeg", Bin: os.Args[0]}.Record(context.Background(), make(chan struct{}))
	if err == nil || !strings.Contains(err.Error(), "ffmpeg) did not produce audio") {
		t.Errorf("%v", err)
	}
	// No temporary folder can be made.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing"))
	if _, _, err := (ToolRecorder{Name: "arecord"}).Record(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "temporary folder") {
		t.Errorf("%v", err)
	}
}

func TestRecordKillsARecorderThatIgnoresInterrupt(t *testing.T) {
	t.Setenv("VOICE_FAKE_RECORDER", "deaf")
	stop := make(chan struct{})
	go func() { time.Sleep(500 * time.Millisecond); close(stop) }()
	start := time.Now()
	wav, cleanup, err := ToolRecorder{Name: "arecord", Bin: os.Args[0]}.Record(context.Background(), stop)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if d := time.Since(start); d < stopGrace+400*time.Millisecond || d > 10*time.Second {
		t.Errorf("killed after %v", d)
	}
	if !validWAV(wav) {
		t.Error("the audio written before the kill must be kept")
	}
}

func TestDefaultRunnersAndWhisperThroughExec(t *testing.T) {
	t.Setenv("VOICE_FAKE_RECORDER", "echo")
	out, err := execRunner(context.Background(), os.Args[0])
	if err != nil || string(out) != "hello there\n" {
		t.Errorf("%q %v", out, err)
	}
	out, err = combinedRunner(context.Background(), os.Args[0])
	if err != nil || !strings.Contains(string(out), "hello there") || !strings.Contains(string(out), "warning") {
		t.Errorf("%q %v", out, err)
	}
	if text, err := (WhisperCLI{Bin: os.Args[0]}).Transcribe(context.Background(), filepath.Join(t.TempDir(), "r.wav")); err != nil || text != "hello there" {
		t.Errorf("%q %v", text, err)
	}
	t.Setenv("VOICE_FAKE_RECORDER", "fail")
	if _, err := (WhisperCLI{Bin: os.Args[0]}).Transcribe(context.Background(), filepath.Join(t.TempDir(), "r.wav")); err == nil || !strings.Contains(err.Error(), "speech engine failed") {
		t.Errorf("%v", err)
	}
}

func TestTranscribeCancelledReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := WhisperCLI{Run: func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("killed") }}
	if _, err := w.Transcribe(ctx, filepath.Join(t.TempDir(), "r.wav")); !errors.Is(err, context.Canceled) {
		t.Errorf("%v", err)
	}
}

// tarball writes a .tar.gz holding one file under a top-level folder.
func tarball(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "src.tar.gz")
	f, _ := os.Create(p)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "whisper.cpp-1.9.5/CMakeLists.txt", Mode: 0o644, Size: 1})
	_, _ = tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	f.Close()
	return p
}

func TestUnpackAndBuild(t *testing.T) {
	s := setup(t, "cmake", "g++")
	archive := tarball(t)
	s.Run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[0] == "--build" {
			_ = os.MkdirAll(filepath.Join(args[1], "bin"), 0o755)
			_ = os.WriteFile(filepath.Join(args[1], "bin", "whisper-cli"), []byte("bin"), 0o644)
		} else if _, err := os.Stat(filepath.Join(args[1], "CMakeLists.txt")); err != nil {
			t.Error("the source must be unpacked before configuring")
		}
		return nil, nil
	}
	bin, err := s.unpackAndBuild(context.Background(), archive)
	if err != nil || bin != s.EnginePath() {
		t.Fatalf("%q %v", bin, err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Error("the archive is deleted after a successful build")
	}
	if _, err := os.Stat(filepath.Join(s.Dirs.Data, "engine", "whisper-"+whisperVersion+".build")); !os.IsNotExist(err) {
		t.Error("the build folder must be removed")
	}
}

func TestUnpackAndBuildFailures(t *testing.T) {
	s := setup(t)
	bad := filepath.Join(t.TempDir(), "bad.tar.gz")
	_ = os.WriteFile(bad, []byte("not a tarball"), 0o600)
	if _, err := s.unpackAndBuild(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "could not unpack") || !strings.Contains(err.Error(), bad) {
		t.Errorf("%v", err)
	}
	var lines []string
	for i := 1; i <= 15; i++ {
		lines = append(lines, "line"+string(rune('a'+i)))
	}
	s.Run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(strings.Join(lines, "\n")), errors.New("exit 2")
	}
	archive := tarball(t)
	_, err := s.unpackAndBuild(context.Background(), archive)
	if err == nil || !strings.Contains(err.Error(), "building the speech engine failed") || !strings.Contains(err.Error(), lines[14]) || strings.Contains(err.Error(), lines[0]+"\n") {
		t.Errorf("%v", err)
	}
	if _, statErr := os.Stat(archive); statErr != nil {
		t.Error("the archive must stay after a failed build")
	}
}

func TestBuildFileErrors(t *testing.T) {
	s := setup(t)
	dir := t.TempDir()
	build := filepath.Join(dir, "build")
	s.Run = func(context.Context, string, ...string) ([]byte, error) { return nil, nil }
	// The build produced no binary.
	if err := s.build(context.Background(), dir, build, filepath.Join(dir, "out", "whisper-cli")); err == nil || !strings.Contains(err.Error(), "produced no speech engine") {
		t.Errorf("%v", err)
	}
	_ = os.MkdirAll(filepath.Join(build, "bin"), 0o755)
	_ = os.WriteFile(filepath.Join(build, "bin", "whisper-cli"), []byte("bin"), 0o644)
	// The install folder cannot be created: its parent is a file.
	blocker := filepath.Join(dir, "file")
	_ = os.WriteFile(blocker, nil, 0o600)
	if err := s.build(context.Background(), dir, build, filepath.Join(blocker, "x", "whisper-cli")); err == nil || !strings.Contains(err.Error(), "cannot create") {
		t.Errorf("%v", err)
	}
	// The file cannot be written: the destination is a folder.
	if err := s.build(context.Background(), dir, build, dir); err == nil || !strings.Contains(err.Error(), "cannot save") {
		t.Errorf("%v", err)
	}
}

func TestDownloadFailuresAreExplained(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	s := setup(t, "cmake", "c++", "make")
	s.Client = &http.Client{Transport: redirect{srv.URL}}
	if _, err := s.EnsureEngine(context.Background()); err == nil || !strings.Contains(err.Error(), "could not download the speech engine source") || !strings.Contains(err.Error(), "safeshell voice setup") {
		t.Errorf("%v", err)
	}
	if _, err := s.EnsureModel(context.Background()); err == nil || !strings.Contains(err.Error(), "could not download the speech model") {
		t.Errorf("%v", err)
	}
}

func TestEnsureModelWhenPresent(t *testing.T) {
	s := setup(t)
	_ = os.MkdirAll(filepath.Dir(s.ModelPath()), 0o755)
	f, _ := os.Create(s.ModelPath())
	_ = f.Truncate(modelSize)
	f.Close()
	if p, err := s.EnsureModel(context.Background()); err != nil || p != s.ModelPath() {
		t.Errorf("%q %v", p, err)
	}
}
