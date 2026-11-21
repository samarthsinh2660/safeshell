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
	return Verified(d.ModelPath(m.File), m.SHA, m.Size)
}

// markerPath is the small file next to a verified download that holds its SHA-256.
func markerPath(path string) string { return path + ".sha256" }

// MarkVerified records that the file at path was checked against sha.
func MarkVerified(path, sha string) error {
	return os.WriteFile(markerPath(path), []byte(sha), 0o644)
}

// Verified reports whether the file at path has the pinned size and SHA-256. The marker next to it holds the SHA-256
// the file was last found to have: a marker equal to sha answers true and any other marker answers false, both without
// reading the file again. With no marker (a download from an older version) the file is hashed once and the marker is
// written with the SHA it really has, so a damaged file of the right size is not hashed on every call. A new verified
// download overwrites the marker, which is how a damaged model is fixed.
func Verified(path, sha string, size int64) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() != size {
		return false
	}
	if b, err := os.ReadFile(markerPath(path)); err == nil {
		return string(b) == sha
	}
	actual, err := fileSHA(path, nil)
	if err != nil {
		return false
	}
	_ = MarkVerified(path, actual) // best effort: without it the file is just checked again next time
	return actual == sha
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
		if err := ExtractTarGz(archive, tmp, w); err != nil {
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
	if err := Download(ctx, client, m.URL, d.ModelPath(m.File), m.SHA, m.Size, w); err != nil {
		return err
	}
	return MarkVerified(d.ModelPath(m.File), m.SHA)
}
