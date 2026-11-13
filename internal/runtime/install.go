package runtime

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/models"
)

// EngineInstalled reports whether the build is unpacked and runnable.
func (d Dirs) EngineInstalled(b Build) bool {
	fi, err := os.Stat(d.ServerBin(b))
	return err == nil && fi.Mode()&0o111 != 0
}

// ModelInstalled reports whether the model file is present and complete.
func (d Dirs) ModelInstalled(m models.Model) bool {
	fi, err := os.Stat(d.ModelPath(m.File))
	return err == nil && fi.Size() == m.Size
}

// InstallEngine downloads, verifies and unpacks a build (and its runtime
// libraries). It does nothing if the build is already installed.
func (d Dirs) InstallEngine(ctx context.Context, client *http.Client, b Build, w io.Writer) error {
	if d.EngineInstalled(b) {
		return nil
	}
	fmt.Fprintf(w, "Downloading the local AI engine (llama.cpp %s, %s, %s)...\n", Tag, b.Variant, mb(b.Size()))
	dest := d.EngineDir(b)
	tmp := dest + ".tmp"
	_ = os.RemoveAll(tmp)
	for _, as := range append([]Asset{b.Main}, b.Extra...) {
		archive := filepath.Join(d.Data, "downloads", as.Name)
		if err := Download(ctx, client, as.URL(), archive, as.SHA, as.Size, w); err != nil {
			return err
		}
		if err := ExtractTarGz(archive, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		_ = os.Remove(archive)
	}
	_ = os.RemoveAll(dest)
	return os.Rename(tmp, dest)
}

// InstallPreferred installs the best engine for this machine and, when that
// is a GPU build, the CPU build too, so the CPU is there if the GPU fails.
func (d Dirs) InstallPreferred(ctx context.Context, client *http.Client, goarch, gpu string, w io.Writer) error {
	cands := Candidates(goarch, gpu)
	if len(cands) == 0 {
		return fmt.Errorf("there is no local engine for %s; use a cloud model instead", goarch)
	}
	for i, b := range cands {
		if i == 0 || !b.GPU() {
			if err := d.InstallEngine(ctx, client, b, w); err != nil {
				return err
			}
		}
	}
	return nil
}

// Installed returns every candidate engine that is installed, best first.
func (d Dirs) Installed(goarch, gpu string) []Build {
	var out []Build
	for _, b := range Candidates(goarch, gpu) {
		if d.EngineInstalled(b) {
			out = append(out, b)
		}
	}
	return out
}

// InstallModel downloads and verifies a catalog model.
func (d Dirs) InstallModel(ctx context.Context, client *http.Client, m models.Model, w io.Writer) error {
	if d.ModelInstalled(m) {
		return nil
	}
	fmt.Fprintf(w, "Downloading %s (%s)...\n", m.Title, mb(m.Size))
	return Download(ctx, client, m.URL, d.ModelPath(m.File), m.SHA, m.Size, w)
}
