package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// autorunModel is fakeModel with "autorun" switched on in its settings.
func autorunModel(t *testing.T, reply string) []string {
	t.Helper()
	env := fakeModel(t, reply)
	for _, e := range env {
		if dir, ok := strings.CutPrefix(e, "XDG_CONFIG_HOME="); ok {
			p := filepath.Join(dir, "safeshell", "config.json")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), "{", `{"autorun":true,`, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return env
}

// autorunHome is a home folder for the shell (HOME is where autorun may write) with a work folder in it.
func autorunHome(t *testing.T) (home, env string) {
	t.Helper()
	home = t.TempDir()
	return home, "HOME=" + home
}

func TestAutorunMovesAFileWithoutAsking(t *testing.T) {
	needSandbox(t)
	d, homeEnv := autorunHome(t)
	from, to := filepath.Join(d, "notes.txt"), filepath.Join(d, "backup")
	_ = os.MkdirAll(to, 0o755)
	_ = os.WriteFile(from, []byte("my notes"), 0o644)
	sh := startBash(t, append(autorunModel(t, `{"command":"mv `+from+` `+to+`/","note":"moves the file"}`), homeEnv)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("move my notes into the backup folder\r")
	sh.waitFor(t, "Running it now")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if b, _ := os.ReadFile(filepath.Join(to, "notes.txt")); string(b) == "my notes" {
			break
		}
	}
	if b, _ := os.ReadFile(filepath.Join(to, "notes.txt")); string(b) != "my notes" {
		t.Fatal("the safe command did not run by itself")
	}
	sh.mu.Lock()
	asked := strings.Contains(sh.all.String(), "Run it?")
	sh.mu.Unlock()
	if asked {
		t.Error("autorun still asked")
	}
	sh.send("safeshell history\r")
	sh.waitFor(t, "autorun")
}

func TestAutorunStillAsksBeforeRunningAScript(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	script := filepath.Join(d, "setup.sh")
	marker := filepath.Join(d, "ran")
	_ = os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755)
	sh := startBash(t, autorunModel(t, `{"command":"`+script+`","note":"runs the setup"}`)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("please set the project up\r")
	sh.waitFor(t, "Run it? [Y/n]")
	sh.send("n\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("autorun ran a script without asking")
	}
}

func TestAutorunCanOpenAFolderInTheShellItself(t *testing.T) {
	needSandbox(t)
	d, homeEnv := autorunHome(t)
	proj := filepath.Join(d, "projects")
	_ = os.MkdirAll(proj, 0o755)
	sh := startBash(t, append(autorunModel(t, `{"command":"cd `+proj+`","note":"goes into the folder"}`), homeEnv)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("take me into the projects folder\r")
	sh.waitFor(t, "Running it now")
	sh.send("pwd\r")
	sh.waitFor(t, proj+"\r\n")
}

func TestAutorunStillBlocksADangerousCommand(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	sh := startBash(t, autorunModel(t, `{"command":"rm -rf `+victim+`","note":"removes it"}`)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("get rid of the project folder\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	sh.mu.Lock()
	ran := strings.Contains(sh.all.String(), "Running it now")
	sh.mu.Unlock()
	if ran {
		t.Error("autorun ran a blocked command")
	}
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the dangerous command ran")
	}
}

func TestAutorunStillAsksBeforeOverwritingAHiddenFile(t *testing.T) {
	needSandbox(t)
	d, homeEnv := autorunHome(t)
	rc := filepath.Join(d, ".bashrc")
	_ = os.WriteFile(rc, []byte("original"), 0o644)
	_ = os.WriteFile(filepath.Join(d, "evil"), []byte("evil"), 0o644)
	sh := startBash(t, append(autorunModel(t, `{"command":"mv `+filepath.Join(d, "evil")+` `+rc+`","note":"replaces it"}`), homeEnv)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("please replace my shell settings\r")
	sh.waitFor(t, "Run it? [Y/n]")
	sh.send("n\r")
	sh.waitFor(t, "Nothing was run")
	if b, _ := os.ReadFile(rc); string(b) != "original" {
		t.Fatal("autorun overwrote a hidden file without asking")
	}
}
