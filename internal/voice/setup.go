package voice

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/runtime"
)

// Pinned speech engine. The whisper.cpp release ships no Linux binaries, so
// SafeShell builds the source tarball of this tag.
const (
	whisperVersion = "v1.9.5"
	// whisperTarURL is the GitHub source archive of the tag above.
	whisperTarURL  = "https://github.com/ggml-org/whisper.cpp/archive/refs/tags/v1.9.5.tar.gz"
	whisperTarSHA  = "ff1a9053feb509ff9d7729703355541ae9690073a6b1c40eb692c962e0dc1720"
	whisperTarSize = 9626464
)

// Pinned model, from the whisper.cpp model repository on Hugging Face.
const (
	modelFile = "ggml-base.en.bin"
	modelURL  = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.en.bin"
	modelSize = 147964211
	modelSHA  = "a03779c86df3323075f5e796cb2ce5029f00ec8869eee3fdfb897afe36c6d002"
)

const errNoBuildTools = "Building the speech engine needs cmake and a C++ compiler. On Ubuntu:  sudo apt install build-essential cmake  then run this again."

const retry = "Check your internet connection and disk space, then run  safeshell voice setup  again."

// Setup finds, builds and downloads what voice needs.
type Setup struct {
	Dirs     runtime.Dirs
	LookPath func(string) (string, error)
	Client   *http.Client
	Out      io.Writer
	Run      Runner // runs cmake and returns stdout and stderr together; exec when nil
}

// EnginePath is where the built whisper-cli is kept.
func (s Setup) EnginePath() string {
	return filepath.Join(s.Dirs.Data, "engine", "whisper-"+whisperVersion, "whisper-cli")
}

// ModelPath is where the speech model is kept.
func (s Setup) ModelPath() string { return s.Dirs.ModelPath(modelFile) }

// Status is what is installed.
type Status struct {
	EngineBin      string // the whisper-cli that would run; empty when none
	EnginePath     string // where a built copy is kept
	EngineFromPATH bool
	ModelReady     bool
}

// Ready is true when both the engine and the model are there.
func (st Status) Ready() bool { return st.EngineBin != "" && st.ModelReady }

func (s Setup) Status() Status {
	st := Status{EnginePath: s.EnginePath()}
	if p, err := s.LookPath("whisper-cli"); err == nil {
		st.EngineBin, st.EngineFromPATH = p, true
	} else if fi, err := os.Stat(st.EnginePath); err == nil && fi.Mode()&0o111 != 0 {
		st.EngineBin = st.EnginePath
	}
	fi, err := os.Stat(s.ModelPath())
	st.ModelReady = err == nil && fi.Size() == modelSize
	return st
}

func (s Setup) run() Runner {
	if s.Run != nil {
		return s.Run
	}
	return combinedRunner
}

func combinedRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// EnsureEngine returns a usable whisper-cli, building it if needed.
func (s Setup) EnsureEngine(ctx context.Context) (string, error) {
	if st := s.Status(); st.EngineBin != "" {
		return st.EngineBin, nil
	}
	if _, err := s.LookPath("cmake"); err != nil || !s.hasCompiler() {
		return "", errors.New(errNoBuildTools)
	}
	fmt.Fprintf(s.Out, "Downloading the speech engine source (whisper.cpp %s)...\n", whisperVersion)
	archive := filepath.Join(s.Dirs.Data, "downloads", "whisper.cpp-"+whisperVersion+".tar.gz")
	if err := runtime.Download(ctx, s.Client, whisperTarURL, archive, whisperTarSHA, whisperTarSize, s.Out); err != nil {
		return "", fmt.Errorf("could not download the speech engine source: %w. %s", err, retry)
	}
	work := filepath.Join(s.Dirs.Data, "engine", "whisper-"+whisperVersion+".build")
	_ = os.RemoveAll(work)
	defer os.RemoveAll(work)
	src := filepath.Join(work, "src")
	if err := runtime.ExtractTarGz(archive, src); err != nil {
		return "", fmt.Errorf("could not unpack the speech engine source: %w. Delete %s and run  safeshell voice setup  again", err, archive)
	}
	fmt.Fprintln(s.Out, "Building the speech engine. This takes a few minutes...")
	if err := s.build(ctx, src, filepath.Join(work, "build"), s.EnginePath()); err != nil {
		return "", err
	}
	_ = os.Remove(archive)
	return s.EnginePath(), nil
}

func (s Setup) hasCompiler() bool {
	for _, c := range []string{"c++", "g++", "clang++"} {
		if _, err := s.LookPath(c); err == nil {
			return true
		}
	}
	return false
}

// build runs cmake on src and copies whisper-cli to dest.
func (s Setup) build(ctx context.Context, src, build, dest string) error {
	steps := [][]string{
		{"-S", src, "-B", build, "-DBUILD_SHARED_LIBS=OFF", "-DWHISPER_BUILD_TESTS=OFF", "-DWHISPER_BUILD_SERVER=OFF", "-DGGML_NATIVE=OFF", "-DCMAKE_BUILD_TYPE=Release"},
		{"--build", build, "--target", "whisper-cli", "-j", strconv.Itoa(goruntime.NumCPU())},
	}
	for _, args := range steps {
		if out, err := s.run()(ctx, "cmake", args...); err != nil {
			return fmt.Errorf("building the speech engine failed: %w\n%s", err, tail(out))
		}
	}
	data, err := os.ReadFile(filepath.Join(build, "bin", "whisper-cli"))
	if err != nil {
		return fmt.Errorf("the build finished but produced no speech engine: %w. Run  safeshell voice setup  again", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return fmt.Errorf("cannot create %s: %w. Check the folder is writable", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, data, 0o755); err != nil {
		return fmt.Errorf("cannot save the speech engine to %s: %w. Check disk space and permissions", dest, err)
	}
	return nil
}

func tail(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	return strings.Join(lines, "\n")
}

// EnsureModel downloads the speech model if it is not there.
func (s Setup) EnsureModel(ctx context.Context) (string, error) {
	if s.Status().ModelReady {
		return s.ModelPath(), nil
	}
	fmt.Fprintf(s.Out, "Downloading the English speech model (%d MB)...\n", modelSize/1_000_000)
	if err := runtime.Download(ctx, s.Client, modelURL, s.ModelPath(), modelSHA, modelSize, s.Out); err != nil {
		return "", fmt.Errorf("could not download the speech model: %w. %s", err, retry)
	}
	return s.ModelPath(), nil
}
