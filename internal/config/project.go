package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// ProjectFile is the name of a project's own settings file.
const ProjectFile = ".safeshell.json"

// LoadProject reads the nearest .safeshell.json in dir or a folder above it,
// stopping (without reading) at home or /. A file that could not be used gives
// an empty project and a warning; the ones about single keys are returned too.
// The file belongs to whoever made the project, so it is read as untrusted:
// see policy.ParseProject.
func LoadProject(dir, home string) (p policy.Project, warnings []string) {
	for d := filepath.Clean(dir); d != home && d != "/" && d != "."; d = filepath.Dir(d) {
		file := filepath.Join(d, ProjectFile)
		fi, err := os.Lstat(file)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err == nil && !fi.Mode().IsRegular() {
			err = errors.New("it is not an ordinary file")
		}
		var b []byte
		if err == nil {
			b, err = readSmall(file)
		}
		if err != nil {
			return policy.Project{}, []string{fmt.Sprintf("the project file %s was ignored because it could not be read (%v). Fix or delete it.", file, err)}
		}
		p, ws := policy.ParseProject(b, d)
		for _, w := range ws {
			warnings = append(warnings, fmt.Sprintf("In %s, %s.", file, w))
		}
		return p, warnings
	}
	return policy.Project{}, nil
}

// readSmall reads a file up to maxPolicyBytes, so a wrong file is never read whole.
func readSmall(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPolicyBytes+1))
	if err == nil && len(b) > maxPolicyBytes {
		err = errors.New("it is too big")
	}
	return b, err
}
