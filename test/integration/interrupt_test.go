package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func scratchBase() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return os.TempDir()
}

func TestCtrlCDuringASimulationLeavesNoScratchFolder(t *testing.T) {
	needSandbox(t)
	cmd := exec.Command(ssBin, "simulate", "--timeout", "60s", "sleep 30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pattern := filepath.Join(scratchBase(), "ss-sim-"+strconv.Itoa(cmd.Process.Pid)+"-*")
	deadline := time.Now().Add(10 * time.Second)
	for {
		if m, _ := filepath.Glob(pattern); len(m) > 0 {
			break
		}
		if time.Now().After(deadline) {
			cmd.Process.Kill()
			t.Fatal("the simulation never created its scratch folder")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cmd.Process.Signal(syscall.SIGINT)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 130 {
			t.Errorf("exit = %v, want status 130", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("safeshell did not stop after Ctrl-C")
	}
	if m, _ := filepath.Glob(pattern); len(m) > 0 {
		t.Errorf("scratch folder left behind: %v", m)
	}
}

func TestLeftoverScratchFolderOfAFinishedRunIsSweptAtTheNextRun(t *testing.T) {
	needSandbox(t)
	// A pid above pid_max belongs to no process; the next free pid could be taken
	// by another process before the run sweeps.
	b, err := os.ReadFile("/proc/sys/kernel/pid_max")
	if err != nil {
		t.Skip("no /proc/sys/kernel/pid_max")
	}
	max, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	dead := max + 1
	stale := filepath.Join(scratchBase(), "ss-sim-"+strconv.Itoa(dead)+"-leftover")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stale)
	simulate(t, "true")
	if _, err := os.Stat(stale); err == nil {
		t.Error("a folder left by a process that is gone was not removed")
	}
}
