// Package install adds SafeShell's hook to a shell's startup file, and removes it.
package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellhook"
)

const (
	begin = "# >>> safeshell >>>"
	end   = "# <<< safeshell <<<"
)

// Shell is a supported shell and where its startup file is.
type Shell struct {
	Name string // bash, zsh or fish
	RC   string
}

// Detect picks the shell from $SHELL (a path such as /bin/zsh). home is the user's home folder.
func Detect(shellEnv, home string) (Shell, error) {
	switch filepath.Base(shellEnv) {
	case "bash":
		return Shell{"bash", filepath.Join(home, ".bashrc")}, nil
	case "zsh":
		return Shell{"zsh", filepath.Join(home, ".zshrc")}, nil
	case "fish": // a file of our own in conf.d, which fish reads at start-up
		return Shell{"fish", filepath.Join(home, ".config", "fish", "conf.d", "safeshell.fish")}, nil
	}
	return Shell{}, errors.New("only bash, zsh and fish are supported (your shell is " + filepath.Base(shellEnv) + ")")
}

// block is the startup-file text. It skips the hook when this shell already has it
// (a machine-wide install loaded it first); there is no return, so the rest of the file still runs.
func block(sh Shell, bin string) string {
	if sh.Name == "fish" {
		return begin + "\ntest -x " + shellhook.FishQuote(bin) + "; and test \"$SAFESHELL_HOOK\" != \"$fish_pid\"; and " + shellhook.FishQuote(bin) + " hook fish | source\n" + end + "\n"
	}
	return begin + "\n[ -x " + quote(bin) + " ] && { [ \"${SAFESHELL_HOOK-}\" = \"$$\" ] || eval \"$(" + quote(bin) + " hook " + sh.Name + ")\"; }\n" + end + "\n"
}

func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Change says what Add did to the startup file.
type Change int

const (
	Unchanged Change = iota // the same block was already there
	Added
	Updated // a block for another binary path was replaced
)

// Add puts the hook block in the startup file. It is idempotent, keeps the rest
// of the file untouched, and writes a .safeshell-backup copy the first time.
// A block that differs from the one it would write now (a different binary
// path, for example) is replaced.
func Add(sh Shell, bin string) (Change, error) {
	old, err := os.ReadFile(sh.RC)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Unchanged, err
	}
	if sh.Name == "fish" {
		if err := os.MkdirAll(filepath.Dir(sh.RC), 0o755); err != nil {
			return Unchanged, err
		}
	}
	text := string(old)
	want := block(sh, bin)
	if i, j := span(text); i >= 0 {
		if text[i:j] == want {
			return Unchanged, nil
		}
		return Updated, os.WriteFile(sh.RC, []byte(text[:i]+want+text[j:]), 0o644)
	}
	if len(old) > 0 {
		if _, e := os.Stat(sh.RC + ".safeshell-backup"); e != nil {
			if err := os.WriteFile(sh.RC+".safeshell-backup", old, 0o600); err != nil {
				return Unchanged, err
			}
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += "\n"
	}
	return Added, os.WriteFile(sh.RC, []byte(text+want), 0o644)
}

// span is where the block starts and ends (after its newline) in text, or -1 when there is none.
func span(text string) (i, j int) {
	i = strings.Index(text, begin)
	j = strings.Index(text, end)
	if i < 0 || j < i {
		return -1, -1
	}
	j += len(end)
	if j < len(text) && text[j] == '\n' {
		j++
	}
	return i, j
}

// Binary is the safeshell the block in the startup file runs, or false when there is no block or it is not in our format.
func Binary(sh Shell) (string, bool) {
	b, err := os.ReadFile(sh.RC)
	if err != nil {
		return "", false
	}
	i, j := span(string(b))
	if i < 0 {
		return "", false
	}
	if sh.Name == "fish" {
		rest, ok := strings.CutPrefix(string(b)[i:j], begin+"\ntest -x '")
		if !ok {
			return "", false
		}
		path, _, ok := strings.Cut(rest, "'; and ")
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`).Replace(path), ok
	}
	rest, ok := strings.CutPrefix(string(b)[i:j], begin+"\n[ -x '")
	if !ok {
		return "", false
	}
	path, _, ok := strings.Cut(rest, "' ]")
	return strings.ReplaceAll(path, `'\''`, "'"), ok
}

// Remove deletes the block, leaving everything else as it was.
func Remove(sh Shell) (changed bool, err error) {
	old, err := os.ReadFile(sh.RC)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	text := string(old)
	i, j := span(text)
	if i < 0 {
		return false, nil
	}
	out := strings.TrimRight(text[:i], "\n")
	rest := text[j:]
	switch {
	case out == "":
		out = rest
	case rest != "":
		out += "\n" + rest
	default:
		out += "\n"
	}
	if sh.Name == "fish" && out == "" { // the file was ours alone
		return true, os.Remove(sh.RC)
	}
	return true, os.WriteFile(sh.RC, []byte(out), 0o644)
}

// Installed reports whether the block is in the startup file.
func Installed(sh Shell) bool {
	b, err := os.ReadFile(sh.RC)
	return err == nil && strings.Contains(string(b), begin)
}
