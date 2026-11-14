package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAddAndRemoveReportAStartupFileTheyCannotRead(t *testing.T) {
	sh := Shell{"bash", filepath.Join(t.TempDir(), "rc")}
	if err := os.Mkdir(sh.RC, 0o755); err != nil { // a folder where the file should be
		t.Fatal(err)
	}
	if _, err := Add(sh, "/bin/safeshell"); err == nil {
		t.Error("Add must fail when the startup file cannot be read")
	}
	if _, err := Remove(sh); err == nil {
		t.Error("Remove must fail when the startup file cannot be read")
	}
}

func TestAddFailsWhenTheBackupCannotBeWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permissions do not apply to root")
	}
	dir := t.TempDir()
	sh := Shell{"bash", filepath.Join(dir, "rc")}
	if err := os.WriteFile(sh.RC, []byte("export A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)
	if _, err := Add(sh, "/bin/safeshell"); err == nil {
		t.Error("Add must fail when the backup cannot be written")
	}
}
