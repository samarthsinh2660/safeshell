package runtime

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ExtractTarGz unpacks src into dest, dropping the archive's top-level folder.
// Entries whose names would land outside dest, and links whose text points
// outside it, are refused. Every write goes through os.Root, so a write that
// would pass through a symlink leading outside dest fails too.
func ExtractTarGz(src, dest string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	rt, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer rt.Close()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		_, rel, _ := strings.Cut(strings.TrimPrefix(h.Name, "./"), "/")
		if rel == "" {
			continue
		}
		rel = filepath.Clean(rel)
		target := filepath.Join(root, rel)
		if !within(root, target) {
			return fmt.Errorf("archive entry %q escapes the install folder", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			err = rt.MkdirAll(rel, 0o755)
		case tar.TypeReg:
			err = writeFile(rt, rel, tr, os.FileMode(h.Mode).Perm()|0o600)
		case tar.TypeSymlink:
			if filepath.IsAbs(h.Linkname) || !within(root, filepath.Join(filepath.Dir(target), h.Linkname)) {
				return fmt.Errorf("archive link %q points outside the install folder", h.Name)
			}
			_ = rt.Remove(rel)
			err = rt.Symlink(h.Linkname, rel)
		}
		if err != nil {
			return err
		}
	}
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(filepath.Separator))
}

func writeFile(rt *os.Root, path string, r io.Reader, mode os.FileMode) error {
	if err := rt.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := rt.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
