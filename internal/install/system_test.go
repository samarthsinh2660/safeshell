package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sysWrite(t *testing.T, root, rel, text string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func sysRead(t *testing.T, root, rel string) (string, bool) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	return string(b), err == nil
}

func TestSystemInstallWritesOnlyForShellsThatAreThere(t *testing.T) {
	root := t.TempDir()
	files, err := AddSystem(root, "/usr/bin/safeshell")
	if err != nil || len(files) != 1 {
		t.Fatalf("%v %v", files, err)
	}
	got, ok := sysRead(t, root, "etc/profile.d/safeshell.sh")
	if !ok || !strings.Contains(got, `'/usr/bin/safeshell' hook bash`) || !strings.Contains(got, `case $- in *i*)`) || !strings.Contains(got, `"${SAFESHELL_HOOK-}" = "$$"`) {
		t.Errorf("profile.d file: %q", got)
	}
	for _, rel := range []string{"etc/bash.bashrc", "etc/zsh/zshrc", "etc/fish/conf.d/safeshell.fish"} {
		if _, ok := sysRead(t, root, rel); ok {
			t.Errorf("%s was created for a shell that is not installed", rel)
		}
	}
}

func TestSystemInstallAndRemoveRestoreFilesExactly(t *testing.T) {
	for _, orig := range []string{"export A=1\n", "export A=1", "export A=1\n\n\n", "\n", "line1\nline2\n"} {
		root := t.TempDir()
		sysWrite(t, root, "etc/bash.bashrc", orig)
		sysWrite(t, root, "etc/zsh/zshrc", orig)
		if err := os.MkdirAll(filepath.Join(root, "etc/fish"), 0o755); err != nil {
			t.Fatal(err)
		}
		files, err := AddSystem(root, "/usr/bin/safeshell")
		if err != nil || len(files) != 4 {
			t.Fatalf("%q: %v %v", orig, files, err)
		}
		for _, rel := range []string{"etc/bash.bashrc", "etc/zsh/zshrc"} {
			if got, _ := sysRead(t, root, rel); !strings.HasPrefix(got, orig) || !strings.Contains(got, sysBegin) {
				t.Errorf("%s keeps the old text and adds the block: %q", rel, got)
			}
		}
		if got, _ := sysRead(t, root, "etc/zsh/zshrc"); !strings.Contains(got, "hook zsh") {
			t.Errorf("zsh: %q", got)
		}
		if got, _ := sysRead(t, root, "etc/fish/conf.d/safeshell.fish"); !strings.Contains(got, "status is-interactive; and test -x '/usr/bin/safeshell'") || !strings.Contains(got, "hook fish | source") {
			t.Errorf("fish: %q", got)
		}
		removed, err := RemoveSystem(root)
		if err != nil || len(removed) != 4 {
			t.Fatalf("%q: %v %v", orig, removed, err)
		}
		for _, rel := range []string{"etc/bash.bashrc", "etc/zsh/zshrc"} {
			if got, _ := sysRead(t, root, rel); got != orig {
				t.Errorf("%s: want %q back, got %q", rel, orig, got)
			}
		}
		for _, rel := range []string{"etc/profile.d/safeshell.sh", "etc/fish/conf.d/safeshell.fish"} {
			if _, ok := sysRead(t, root, rel); ok {
				t.Errorf("%s was ours and must be deleted", rel)
			}
		}
	}
}

func TestSystemInstallIsIdempotentAndUpdatesTheBinaryPath(t *testing.T) {
	root := t.TempDir()
	sysWrite(t, root, "etc/bash.bashrc", "export A=1")
	if _, err := AddSystem(root, "/old/safeshell"); err != nil {
		t.Fatal(err)
	}
	first, _ := sysRead(t, root, "etc/bash.bashrc")
	files, err := AddSystem(root, "/old/safeshell")
	if err != nil || len(files) != 0 {
		t.Errorf("second run changed %v %v", files, err)
	}
	if again, _ := sysRead(t, root, "etc/bash.bashrc"); again != first || strings.Count(again, sysBegin) != 1 {
		t.Errorf("second run changed the file: %q", again)
	}
	files, err = AddSystem(root, "/new/safeshell")
	if err != nil || len(files) != 2 {
		t.Errorf("moved binary: %v %v", files, err)
	}
	if got, _ := sysRead(t, root, "etc/bash.bashrc"); strings.Contains(got, "/old/") || !strings.Contains(got, "/new/safeshell") || strings.Count(got, sysBegin) != 1 {
		t.Errorf("%q", got)
	}
	if _, err := RemoveSystem(root); err != nil {
		t.Fatal(err)
	}
	if got, _ := sysRead(t, root, "etc/bash.bashrc"); got != "export A=1" {
		t.Errorf("not restored: %q", got)
	}
}

func TestSystemRemoveDoesNothingWithoutABlock(t *testing.T) {
	root := t.TempDir()
	sysWrite(t, root, "etc/bash.bashrc", "export A=1\n")
	files, err := RemoveSystem(root)
	if err != nil || len(files) != 0 {
		t.Errorf("%v %v", files, err)
	}
	if got, _ := sysRead(t, root, "etc/bash.bashrc"); got != "export A=1\n" {
		t.Errorf("%q", got)
	}
}

func TestSystemFilesThatCannotBeReadAreReported(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "etc/profile.d/safeshell.sh"), 0o755); err != nil { // a folder where the file goes
		t.Fatal(err)
	}
	if _, err := AddSystem(root, "/bin/safeshell"); err == nil {
		t.Error("AddSystem must fail when it cannot read a file")
	}
	if _, err := RemoveSystem(root); err == nil {
		t.Error("RemoveSystem must fail when it cannot read a file")
	}
}

func TestSystemInstallFailsWhenItCannotWrite(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "etc"), 0o555); err != nil { // profile.d cannot be created
		t.Fatal(err)
	}
	if _, err := AddSystem(root, "/bin/safeshell"); err == nil {
		t.Error("AddSystem must fail when it cannot create the folder")
	}
	if err := os.Chmod(filepath.Join(root, "etc"), 0o755); err != nil {
		t.Fatal(err)
	}
	p := sysWrite(t, root, "etc/bash.bashrc", "x\n")
	if _, err := AddSystem(root, "/bin/safeshell"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := AddSystem(root, "/other/safeshell"); err == nil {
		t.Error("AddSystem must fail when it cannot rewrite the block")
	}
	if _, err := RemoveSystem(root); err == nil {
		t.Error("RemoveSystem must fail when it cannot rewrite the file")
	}
}

func TestSystemRemoveFailsWhenItCannotDeleteAFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	root := t.TempDir()
	if _, err := AddSystem(root, "/bin/safeshell"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "etc/profile.d")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := RemoveSystem(root); err == nil {
		t.Error("RemoveSystem must fail when it cannot delete the file")
	}
}

func TestSystemRemoveDeletesAFileThatIsOnlyOurBlockWithoutANewline(t *testing.T) {
	root := t.TempDir()
	p := sysWrite(t, root, "etc/bash.bashrc", sysBegin+"\nx\n"+sysEnd)
	if _, err := RemoveSystem(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("the file held only our block and must be deleted")
	}
}
