package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellhook"
)

const (
	sysBegin = "# >>> safeshell system >>>"
	sysEnd   = "# <<< safeshell system <<<"
)

// systemFile is one machine-wide startup file and the lines SafeShell puts in it.
type systemFile struct {
	path string // under the root
	body string
	need string // this path must exist, or the shell is not installed and the file is skipped ("" = always)
}

// systemFiles are the files every user's shell reads. bash login shells read
// /etc/profile.d; Debian's bash reads /etc/bash.bashrc for the other interactive
// shells (profile.d is not read by a non-login shell). zsh and fish have their own folders.
// A file is only touched when its shell is installed; the hook skips itself when this shell already has it.
func systemFiles(bin string) []systemFile {
	q := quote(bin)
	bash := `[ -n "${BASH_VERSION-}" ] && [ -x ` + q + ` ] && case $- in *i*) [ "${SAFESHELL_HOOK-}" = "$$" ] || eval "$(` + q + ` hook bash)";; esac`
	zsh := `[[ -o interactive && -x ` + q + ` && "${SAFESHELL_HOOK-}" != "$$" ]] && eval "$(` + q + ` hook zsh)"`
	fq := shellhook.FishQuote(bin)
	fish := "status is-interactive; and test -x " + fq + "; and test \"$SAFESHELL_HOOK\" != \"$fish_pid\"; and " + fq + " hook fish | source"
	return []systemFile{
		{"etc/profile.d/safeshell.sh", bash, ""},
		{"etc/bash.bashrc", bash, "etc/bash.bashrc"},
		{"etc/zsh/zshrc", zsh, "etc/zsh"},
		{"etc/fish/conf.d/safeshell.fish", fish, "etc/fish"},
	}
}

// AddSystem installs the hook for every user of the machine under root ("/" in
// real use) and returns the files it changed. It is idempotent. A file the admin
// already has gets the block appended and nothing else changes.
func AddSystem(root, bin string) ([]string, error) {
	var done []string
	for _, f := range systemFiles(bin) {
		if f.need != "" {
			if _, err := os.Stat(filepath.Join(root, f.need)); err != nil {
				continue
			}
		}
		p := filepath.Join(root, f.path)
		changed, err := setBlock(p, sysBegin+"\n"+f.body+"\n"+sysEnd)
		if err != nil {
			return done, err
		}
		if changed {
			done = append(done, p)
		}
	}
	return done, nil
}

// RemoveSystem undoes AddSystem: the block comes out of each file, which is then
// exactly as it was, and a file that holds nothing else is deleted. It returns the files it changed.
func RemoveSystem(root string) ([]string, error) {
	var done []string
	for _, f := range systemFiles("") {
		p := filepath.Join(root, f.path)
		changed, err := dropBlock(p)
		if err != nil {
			return done, err
		}
		if changed {
			done = append(done, p)
		}
	}
	return done, nil
}

// setBlock appends the block to the file (creating it), or replaces a different block in place.
// A file that did not end in a newline gets the block without a final newline, which dropBlock reads back.
func setBlock(path, want string) (bool, error) {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, err
	}
	text := string(old)
	if i, j := blockSpan(text); i >= 0 {
		if text[i:j] == want {
			return false, nil
		}
		return true, os.WriteFile(path, []byte(text[:i]+want+text[j:]), 0o644)
	}
	if text == "" || strings.HasSuffix(text, "\n") {
		text += want + "\n"
	} else {
		text += "\n" + want
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, []byte(text), 0o644)
}

func dropBlock(path string) (bool, error) {
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text := string(old)
	i, j := blockSpan(text)
	if i < 0 {
		return false, nil
	}
	switch {
	case j < len(text): // a newline follows the block
		text = text[:i] + text[j+1:]
	case i > 0: // the block ended the file without one, so the newline before it was ours
		text = text[:i-1]
	default:
		text = ""
	}
	if text == "" { // the file was ours alone
		return true, os.Remove(path)
	}
	return true, os.WriteFile(path, []byte(text), 0o644)
}

// blockSpan is where the system block starts and where its last character ends, or -1 when there is none.
func blockSpan(text string) (i, j int) {
	i = strings.Index(text, sysBegin)
	j = strings.Index(text, sysEnd)
	if i < 0 || j < i {
		return -1, -1
	}
	return i, j + len(sysEnd)
}
