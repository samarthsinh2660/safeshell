package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/samarthsinh2660/safeshell/internal/policy"
)

// PolicyPath is the admin's team policy. It is not user-configurable, so a user
// cannot point SafeShell at a policy of their own.
const PolicyPath = "/etc/safeshell/policy.json"

// maxPolicyBytes keeps a wrong file (a log, a device) from being read whole.
const maxPolicyBytes = 1 << 20

// LoadPolicy reads the team policy at path. found is false when there is no
// such file. The file and the folder holding it must belong to owner (root in
// real use) and be writable by nobody else; a policy anyone could edit is
// refused, with what to run to fix it.
func LoadPolicy(path string, owner uint32) (rules []policy.Rule, found bool, err error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("cannot read the team policy %s: %w", path, err)
	}
	di, _ := os.Stat(filepath.Dir(path)) // if it vanished, nil is not trusted
	for _, st := range []fs.FileInfo{fi, di} {
		if !trusted(st, owner) {
			return nil, true, fmt.Errorf("the team policy %s was ignored because it could be changed by someone other than the administrator. Fix it with: sudo chown root:root %s %s && sudo chmod go-w %s %s",
				path, path, filepath.Dir(path), path, filepath.Dir(path))
		}
	}
	f, err := os.Open(path)
	if err != nil { // in practice a file only root can read
		return nil, true, fmt.Errorf("cannot read the team policy %s (%w). Everyone must be able to read it: sudo chmod 644 %s", path, err, path)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxPolicyBytes+1))
	if err != nil {
		return nil, true, fmt.Errorf("cannot read the team policy %s: %w", path, err)
	}
	if len(b) > maxPolicyBytes {
		return nil, true, fmt.Errorf("the team policy %s is too big to be a policy file", path)
	}
	rules, err = policy.Parse(b)
	if err != nil {
		return nil, true, fmt.Errorf("the team policy %s was ignored: %w", path, err)
	}
	return rules, true, nil
}

// trusted: owned by owner and not writable by group or others.
func trusted(fi fs.FileInfo, owner uint32) bool {
	if fi == nil {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && st.Uid == owner && fi.Mode().Perm()&0o022 == 0
}
