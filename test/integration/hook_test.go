package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// term is a bash session on a pseudo-terminal, as a person would use it.
type term struct {
	master *os.File
	mu     sync.Mutex
	buf    strings.Builder
}

func startBash(t *testing.T, extraEnv ...string) *term {
	return startShell(t, []string{"bash", "--norc", "--noprofile", "-i"}, extraEnv...)
}

func startShell(t *testing.T, argv []string, extraEnv ...string) *term {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skip("no pty:", err)
	}
	n, _ := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	_ = unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0)
	slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skip("no pty:", err)
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = append(os.Environ(), "PS1=READY> ", "XDG_STATE_HOME="+t.TempDir(), "XDG_DATA_HOME="+t.TempDir(), "PATH="+filepath.Dir(ssBin)+":"+os.Getenv("PATH"))
	c.Env = append(c.Env, extraEnv...) // last, so a test can point the shell at its own folders
	c.Stdin, c.Stdout, c.Stderr = slave, slave, slave
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	slave.Close()
	tm := &term{master: m}
	go func() {
		b := make([]byte, 4096)
		for {
			n, err := m.Read(b)
			if n > 0 {
				tm.mu.Lock()
				tm.buf.Write(b[:n])
				tm.mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { _ = c.Process.Kill(); _ = c.Wait(); m.Close() })
	return tm
}

// send types s, after forgetting what was on screen so waitFor sees only new output.
func (tm *term) send(s string) {
	tm.mu.Lock()
	tm.buf.Reset()
	tm.mu.Unlock()
	_, _ = tm.master.WriteString(s)
}

func (tm *term) waitFor(t *testing.T, want string) {
	t.Helper()
	for end := time.Now().Add(15 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		tm.mu.Lock()
		ok := strings.Contains(tm.buf.String(), want)
		tm.mu.Unlock()
		if ok {
			return
		}
	}
	tm.mu.Lock()
	defer tm.mu.Unlock()
	t.Fatalf("never saw %q; terminal showed:\n%s", want, tm.buf.String())
}

func TestShellHookStopsADangerousCommandAndLetsSafeOnesRun(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)

	sh := startBash(t)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the dangerous command ran")
	}

	sh.send("echo from-the-hook\r")
	sh.waitFor(t, "from-the-hook\r\n")

	// "Do it anyway" really runs it.
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("d\r")
	sh.waitFor(t, "Type yes")
	sh.send("yes\r")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(victim); err != nil {
			break
		}
	}
	if _, err := os.Stat(victim); err == nil {
		t.Fatal("do-it-anyway did not run the command")
	}

	// ...and undo brings it back, verified.
	sh.send("safeshell undo\r")
	sh.waitFor(t, "Verified")
	if b, err := os.ReadFile(filepath.Join(victim, "a.txt")); err != nil || string(b) != "x" {
		t.Errorf("undo did not restore the file: %v %q", err, b)
	}
}

// fakeModel installs a stand-in llama-server and a sparse model file where
// SafeShell looks for them, so the whole local path (find engine, start it,
// wait for health, chat) runs for real. It returns the environment to use.
func fakeModel(t *testing.T, reply string) []string {
	t.Helper()
	data, cfg, state := t.TempDir(), t.TempDir(), t.TempDir()
	engine := filepath.Join(data, "safeshell", "engine", "b11429-cpu")
	_ = os.MkdirAll(engine, 0o755)
	build := exec.Command("go", "build", "-o", filepath.Join(engine, "llama-server"), "./fakellama")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake engine: %v\n%s", err, out)
	}
	_ = os.MkdirAll(filepath.Join(data, "safeshell", "models"), 0o755)
	model := filepath.Join(data, "safeshell", "models", "qwen2.5-coder-1.5b-instruct-q4_k_m.gguf")
	if err := os.WriteFile(model, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(model, 1117320768); err != nil { // the catalog size; sparse, so it costs no disk
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(cfg, "safeshell"), 0o755)
	_ = os.WriteFile(filepath.Join(cfg, "safeshell", "config.json"),
		[]byte(`{"ai":{"mode":"local","local":{"model":"qwen2.5-coder-1.5b","gpu":"off"}}}`), 0o600)
	env := []string{"XDG_DATA_HOME=" + data, "XDG_CONFIG_HOME=" + cfg, "XDG_STATE_HOME=" + state, "FAKE_REPLY=" + reply}
	t.Cleanup(func() {
		c := exec.Command(ssBin, "ai", "stop")
		c.Env = append(os.Environ(), env...)
		_ = c.Run()
	})
	return env
}

func TestTypingEnglishInTheTerminalRunsTheTranslatedCommand(t *testing.T) {
	needSandbox(t)
	sh := startBash(t, fakeModel(t, `{"command":"echo translated-by-model","note":"prints a word"}`)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("please say something nice\r")
	sh.waitFor(t, "Proposed: echo translated-by-model")
	sh.waitFor(t, "Run it? [Y/n]")
	sh.send("\r")
	sh.waitFor(t, "translated-by-model\r\n")

	// The same works with the explicit ? prefix, and a real command is untouched.
	sh.send("? anything at all\r")
	sh.waitFor(t, "Run it? [Y/n]")
	sh.send("n\r")
	sh.waitFor(t, "Nothing was run")
	sh.send("echo plain-command\r")
	sh.waitFor(t, "plain-command\r\n")
}

func TestADangerousTranslationIsStillStopped(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	sh := startBash(t, fakeModel(t, `{"command":"rm -rf `+victim+`","note":"removes it"}`)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("get rid of the project folder\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the model's command ran without passing the safety check")
	}
}

func TestSlashCommandsWorkInsideTheTerminal(t *testing.T) {
	needSandbox(t)
	sh := startBash(t, fakeModel(t, `{"command":"true"}`)...)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("/help start\r")
	sh.waitFor(t, "Get started in two minutes")
	sh.send("/models\r")
	sh.waitFor(t, "SafeShell AI models")
	sh.waitFor(t, "Gemini")
	sh.send("q\r")
	sh.waitFor(t, "READY>")
	sh.send("echo still-works\r")
	sh.waitFor(t, "still-works\r\n")
}

func TestCtrlJCannotBypassTheGuard(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	sh := startBash(t)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("rm -rf " + victim + "\n") // a bare newline is Ctrl-J, which readline also treats as Enter
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("Ctrl-J ran the command without the guard")
	}
}

func TestIfSafeShellItselfFailsTheLineRunsWithAWarningInsteadOfVanishing(t *testing.T) {
	needSandbox(t)
	fake := t.TempDir()
	_ = os.WriteFile(filepath.Join(fake, "safeshell"), []byte("#!/bin/sh\nexit 7\n"), 0o755)
	// The broken `safeshell` is first in PATH from the start; the hook itself comes from the real
	// binary. (Typing the PATH change would be checked, and a /tmp folder first in PATH asks.)
	sh := startBash(t, "PATH="+fake+":"+filepath.Dir(ssBin)+":"+os.Getenv("PATH"))
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(` + ssBin + ` hook bash)"` + "\r")
	sh.send("echo still-ran\r")
	sh.waitFor(t, "NOT checked")
	sh.waitFor(t, "still-ran\r\n")
}

func TestZshHookGuardsEveryWayOfSubmittingALine(t *testing.T) {
	needSandbox(t)
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh is not installed")
	}
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	sh := startShell(t, []string{"zsh", "-f", "-i"})
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook zsh)"` + "\r")
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("zsh ran the dangerous command")
	}
	sh.send("echo zsh-still-works\r")
	sh.waitFor(t, "zsh-still-works\r\n")
}

func TestCtrlOCannotBypassTheGuardEither(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	victim := filepath.Join(d, "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)
	sh := startBash(t)
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("rm -rf " + victim + "\x0f") // Ctrl-O: run this line and bring up the next history entry
	sh.waitFor(t, "[c] cancel")
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("Ctrl-O ran the command without the guard")
	}
}
