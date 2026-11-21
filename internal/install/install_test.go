package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rc(t *testing.T, content string) Shell {
	p := filepath.Join(t.TempDir(), ".bashrc")
	if content != "\x00" {
		os.WriteFile(p, []byte(content), 0o600)
	}
	return Shell{"bash", p}
}

func read(t *testing.T, sh Shell) string {
	b, _ := os.ReadFile(sh.RC)
	return string(b)
}

func TestDetect(t *testing.T) {
	if s, err := Detect("/usr/bin/zsh", "/h"); err != nil || s.Name != "zsh" || s.RC != "/h/.zshrc" {
		t.Errorf("%+v %v", s, err)
	}
	if s, _ := Detect("/bin/bash", "/h"); s.RC != "/h/.bashrc" {
		t.Error(s)
	}
	if _, err := Detect("/usr/bin/nu", "/h"); err == nil || !strings.Contains(err.Error(), "nu") {
		t.Errorf("%v", err)
	}
}

func TestAddKeepsTheExistingFileBacksItUpAndIsIdempotent(t *testing.T) {
	orig := "export A=1\nalias ll='ls -l'"
	sh := rc(t, orig)
	change, err := Add(sh, "/home/me/.local/bin/safeshell")
	changed := change == Added
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	got := read(t, sh)
	if !strings.HasPrefix(got, orig+"\n") || !strings.Contains(got, `hook bash)"`) || !strings.Contains(got, "'/home/me/.local/bin/safeshell'") {
		t.Errorf("%q", got)
	}
	if b, _ := os.ReadFile(sh.RC + ".safeshell-backup"); string(b) != orig {
		t.Error("no faithful backup")
	}
	if change, _ := Add(sh, "/home/me/.local/bin/safeshell"); change != Unchanged || read(t, sh) != got {
		t.Error("adding twice must change nothing")
	}
	if !Installed(sh) {
		t.Error("Installed")
	}
}

func TestAddCreatesAMissingFileAndQuotesOddPaths(t *testing.T) {
	sh := rc(t, "\x00")
	if _, err := Add(sh, "/home/o'brien/safeshell"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, sh), `'/home/o'\''brien/safeshell'`) {
		t.Errorf("%q", read(t, sh))
	}
}

func TestRemoveRestoresTheFileExactly(t *testing.T) {
	orig := "export A=1\n\nalias x=y\n"
	sh := rc(t, orig)
	Add(sh, "/safeshell")
	if changed, err := Remove(sh); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if got := read(t, sh); got != "export A=1\n\nalias x=y\n" {
		t.Errorf("%q", got)
	}
	if changed, _ := Remove(sh); changed {
		t.Error("removing twice")
	}
	if Installed(sh) {
		t.Error("still installed")
	}
	if changed, err := Remove(rc(t, "\x00")); changed || err != nil {
		t.Error("missing file")
	}
}

func TestRemoveKeepsLinesAfterTheBlock(t *testing.T) {
	sh := rc(t, "")
	Add(sh, "/safeshell")
	os.WriteFile(sh.RC, []byte(read(t, sh)+"export AFTER=1\n"), 0o644)
	Remove(sh)
	if got := read(t, sh); got != "export AFTER=1\n" {
		t.Errorf("%q", got)
	}
}

func TestAddReplacesABlockForAnotherBinaryAndKeepsTheRest(t *testing.T) {
	sh := rc(t, "export A=1\n")
	Add(sh, "/home/me/.local/bin/safeshell")
	os.WriteFile(sh.RC, []byte(read(t, sh)+"export AFTER=1\n"), 0o644)
	change, err := Add(sh, "/usr/bin/safeshell")
	if err != nil || change != Updated {
		t.Fatal(change, err)
	}
	got := read(t, sh)
	if strings.Contains(got, ".local") || !strings.HasPrefix(got, "export A=1\n") || !strings.HasSuffix(got, "export AFTER=1\n") || strings.Count(got, begin) != 1 {
		t.Errorf("%q", got)
	}
	if bin, ok := Binary(sh); !ok || bin != "/usr/bin/safeshell" {
		t.Errorf("Binary = %q %v", bin, ok)
	}
	if change, _ := Add(sh, "/usr/bin/safeshell"); change != Unchanged {
		t.Error("the same block must change nothing")
	}
}

func TestBinaryReadsBackQuotedPathsAndRefusesOtherFormats(t *testing.T) {
	sh := rc(t, "\x00")
	if _, ok := Binary(sh); ok {
		t.Error("no file")
	}
	Add(sh, "/home/o'brien/safeshell")
	if bin, ok := Binary(sh); !ok || bin != "/home/o'brien/safeshell" {
		t.Errorf("%q %v", bin, ok)
	}
	os.WriteFile(sh.RC, []byte("export A=1\n"), 0o644)
	if _, ok := Binary(sh); ok {
		t.Error("no block")
	}
	os.WriteFile(sh.RC, []byte(begin+"\neval hook\n"+end+"\n"), 0o644)
	if _, ok := Binary(sh); ok {
		t.Error("a block in another format")
	}
	os.WriteFile(sh.RC, []byte(begin+"\n[ -x 'broken\n"+end+"\n"), 0o644)
	if _, ok := Binary(sh); ok {
		t.Error("unterminated path")
	}
}

func fishShell(t *testing.T) Shell {
	t.Helper()
	sh, err := Detect("/usr/bin/fish", t.TempDir())
	if err != nil || sh.Name != "fish" || !strings.HasSuffix(sh.RC, "/.config/fish/conf.d/safeshell.fish") {
		t.Fatalf("%+v %v", sh, err)
	}
	return sh
}

func TestFishGetsItsOwnFileInConfDAndUninstallDeletesIt(t *testing.T) {
	sh := fishShell(t) // the conf.d folder does not exist yet
	if change, err := Add(sh, "/home/me/.local/bin/safeshell"); err != nil || change != Added {
		t.Fatal(change, err)
	}
	want := begin + "\ntest -x '/home/me/.local/bin/safeshell'; and test \"$SAFESHELL_HOOK\" != \"$fish_pid\"; and '/home/me/.local/bin/safeshell' hook fish | source\n" + end + "\n"
	if got := read(t, sh); got != want {
		t.Errorf("%q", got)
	}
	if change, _ := Add(sh, "/home/me/.local/bin/safeshell"); change != Unchanged {
		t.Error("adding twice must change nothing")
	}
	if change, _ := Add(sh, "/usr/bin/safeshell"); change != Updated || !strings.Contains(read(t, sh), "'/usr/bin/safeshell' hook fish") {
		t.Error("another binary path must update the block")
	}
	if !Installed(sh) {
		t.Error("Installed")
	}
	if changed, err := Remove(sh); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if _, err := os.Stat(sh.RC); !os.IsNotExist(err) {
		t.Error("the file held only our block, so it must be deleted")
	}
}

func TestFishRemoveKeepsOtherLinesInTheFile(t *testing.T) {
	sh := fishShell(t)
	Add(sh, "/safeshell")
	os.WriteFile(sh.RC, []byte("set -x A 1\n"+read(t, sh)), 0o644)
	if changed, err := Remove(sh); err != nil || !changed {
		t.Fatal(changed, err)
	}
	if got := read(t, sh); got != "set -x A 1\n" {
		t.Errorf("%q", got)
	}
}

func TestFishBinaryReadsBackQuotedPathsAndRefusesOtherFormats(t *testing.T) {
	sh := fishShell(t)
	for _, bin := range []string{"/home/o'brien/safeshell", `/home/back\slash/safeshell`, `/a\'b/safeshell`} {
		Add(sh, bin)
		if got, ok := Binary(sh); !ok || got != bin {
			t.Errorf("%q: got %q %v", bin, got, ok)
		}
	}
	os.WriteFile(sh.RC, []byte(begin+"\nsafeshell hook fish | source\n"+end+"\n"), 0o644)
	if _, ok := Binary(sh); ok {
		t.Error("a block in another format")
	}
	os.WriteFile(sh.RC, []byte(begin+"\ntest -x 'broken\n"+end+"\n"), 0o644)
	if _, ok := Binary(sh); ok {
		t.Error("unterminated path")
	}
}

func TestFishAddFailsWhenConfDCannotBeCreated(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	sh := Shell{"fish", filepath.Join(dir, "conf.d", "safeshell.fish")}
	if _, err := Add(sh, "/safeshell"); err == nil {
		t.Error("Add must say when the folder cannot be created")
	}
}

func TestUserBlockSkipsTheHookWhenThisShellAlreadyHasIt(t *testing.T) {
	for name, want := range map[string]string{
		"bash": `[ -x '/b/ss' ] && { [ "${SAFESHELL_HOOK-}" = "$$" ] || eval "$('/b/ss' hook bash)"; }`,
		"zsh":  `[ -x '/b/ss' ] && { [ "${SAFESHELL_HOOK-}" = "$$" ] || eval "$('/b/ss' hook zsh)"; }`,
		"fish": `test -x '/b/ss'; and test "$SAFESHELL_HOOK" != "$fish_pid"; and '/b/ss' hook fish | source`,
	} {
		got := block(Shell{Name: name}, "/b/ss")
		if got != begin+"\n"+want+"\n"+end+"\n" || strings.Contains(got, "return") {
			t.Errorf("%s: %q", name, got)
		}
	}
}

func TestAddReplacesABlockWrittenBeforeTheSkipCheck(t *testing.T) {
	old := begin + "\n[ -x '/b/ss' ] && eval \"$('/b/ss' hook bash)\"\n" + end + "\n"
	sh := rc(t, "export A=1\n\n"+old)
	if change, err := Add(sh, "/b/ss"); err != nil || change != Updated {
		t.Fatal(change, err)
	}
	got := read(t, sh)
	if strings.Contains(got, "&& eval") || !strings.Contains(got, `${SAFESHELL_HOOK-}`) || strings.Count(got, begin) != 1 || !strings.HasPrefix(got, "export A=1\n") {
		t.Errorf("%q", got)
	}
	if bin, ok := Binary(sh); !ok || bin != "/b/ss" {
		t.Errorf("Binary: %q %v", bin, ok)
	}
}
