package integration

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPty returns both ends of a new pseudo terminal.
func openPty(t *testing.T) (master, slave *os.File) {
	t.Helper()
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo terminal here: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	if err := unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("cannot unlock pseudo terminal: %v", err)
	}
	n, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("cannot name pseudo terminal: %v", err)
	}
	slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("cannot open pseudo terminal: %v", err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestCommandCannotWriteToTheRealTerminal(t *testing.T) {
	needSandbox(t)
	master, slave := openPty(t)

	// The marker is built at run time so the command text does not contain it.
	cmd := exec.Command(ssBin, "simulate", "--json", `m=LEAK; echo ${m}_MARKER > /dev/tty`)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	done := make(chan struct{})
	go func() { io.Copy(&out, master); close(done) }()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("safeshell simulate: %v\n%s", err, out.String())
	}
	slave.Close() // lets the reader see the end
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
	if strings.Contains(out.String(), "LEAK_MARKER") {
		t.Errorf("the command reached the real terminal:\n%s", out.String())
	}
}

func TestSandboxHasNoTerminalDevice(t *testing.T) {
	needSandbox(t)
	r := simulate(t, `test -e /dev/tty && echo present || echo absent`)
	if got := strings.TrimSpace(r.Stdout); got != "absent" {
		t.Errorf("/dev/tty should not exist inside the sandbox, got %q", got)
	}
}
