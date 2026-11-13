// Package integration runs the real safeshell binary against the real kernel. It needs
// bin/safeshell (make build) and a machine where `safeshell doctor` reports the sandbox as
// available; otherwise every test is skipped.
package integration

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type change struct {
	Path  string
	Kind  string
	IsDir bool
	Files int
	Bytes int64
}

type result struct {
	ExitCode  int
	TimedOut  bool
	Stdout    string
	Stderr    string
	Diff      struct{ Changes []change }
	Truncated bool
	Warnings  []struct {
		Paths []string
		Text  string
	}
	Net []struct {
		Family, Addr string
		Port, Count  int
	}
}

var ssBin string

func TestMain(m *testing.M) {
	ssBin, _ = filepath.Abs("../../bin/safeshell")
	os.Exit(m.Run())
}

func needSandbox(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(ssBin); err != nil {
		t.Skip("bin/safeshell not built (run: make build)")
	}
	if err := exec.Command(ssBin, "doctor").Run(); err != nil {
		t.Skip("sandbox not available here (run: safeshell doctor)")
	}
}

func simulate(t *testing.T, args ...string) result {
	t.Helper()
	out, err := exec.Command(ssBin, append([]string{"simulate", "--json"}, args...)...).Output()
	if err != nil {
		t.Fatalf("safeshell simulate %v: %v\n%s", args, err, out)
	}
	var r result
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	return r
}

func (r result) find(rel string) (change, bool) {
	for _, c := range r.Diff.Changes {
		if strings.HasSuffix(c.Path, rel) {
			return c, true
		}
	}
	return change{}, false
}

func fixture(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(d, "proj", "sub"), 0o755))
	must(os.WriteFile(filepath.Join(d, "proj", "a.txt"), []byte("aaaa"), 0o644))
	must(os.WriteFile(filepath.Join(d, "proj", "sub", "b.txt"), []byte("bb"), 0o644))
	must(os.WriteFile(filepath.Join(d, "keep.txt"), []byte("keep"), 0o644))
	return d
}

func TestDeleteIsObservedAndRealFilesSurvive(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	r := simulate(t, "--cwd", d, "rm -rf proj")
	c, ok := r.find("/proj")
	if !ok || c.Kind != "deleted" || !c.IsDir || c.Files != 2 || c.Bytes != 6 {
		t.Fatalf("deleted dir not observed correctly: %+v (all: %+v)", c, r.Diff.Changes)
	}
	for _, f := range []string{"proj/a.txt", "proj/sub/b.txt", "keep.txt"} {
		if _, err := os.Stat(filepath.Join(d, f)); err != nil {
			t.Errorf("real file %s was touched by the simulation: %v", f, err)
		}
	}
}

func TestCreateAndModifyAreObserved(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	r := simulate(t, "--cwd", d, "echo hi > new.txt; echo more >> keep.txt")
	if c, ok := r.find("/new.txt"); !ok || c.Kind != "created" {
		t.Errorf("new.txt: %+v", c)
	}
	if c, ok := r.find("/keep.txt"); !ok || c.Kind != "modified" {
		t.Errorf("keep.txt: %+v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "keep.txt")); string(b) != "keep" {
		t.Errorf("real keep.txt changed to %q", b)
	}
	if _, err := os.Stat(filepath.Join(d, "new.txt")); err == nil {
		t.Error("new.txt exists on the real filesystem")
	}
}

func TestWriteToHomeNeverReachesTheRealHome(t *testing.T) {
	needSandbox(t)
	home, _ := os.UserHomeDir()
	name := ".ss-sim-canary-" + time.Now().Format("150405.000")
	r := simulate(t, "echo x > ~/"+name)
	if _, ok := r.find(name); !ok {
		t.Errorf("canary not in the diff: %+v", r.Diff.Changes)
	}
	if _, err := os.Stat(filepath.Join(home, name)); err == nil {
		os.Remove(filepath.Join(home, name))
		t.Fatal("the simulation wrote into the real home directory")
	}
}

func TestExitCodeStdoutAndStderrAreReported(t *testing.T) {
	needSandbox(t)
	r := simulate(t, "echo out; echo err >&2; exit 7")
	if r.ExitCode != 7 || strings.TrimSpace(r.Stdout) != "out" || strings.TrimSpace(r.Stderr) != "err" {
		t.Errorf("%+v", r)
	}
}

func TestTimeoutKillsTheWholeTree(t *testing.T) {
	needSandbox(t)
	start := time.Now()
	r := simulate(t, "--timeout", "1s", "sleep 30 & sleep 30; echo unreachable")
	if !r.TimedOut || r.ExitCode != -1 || strings.Contains(r.Stdout, "unreachable") {
		t.Errorf("%+v", r)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %s: background processes were not killed", time.Since(start))
	}
}

func TestNetworkIsUnreachable(t *testing.T) {
	needSandbox(t)
	r := simulate(t, `python3 -c "import socket; socket.create_connection(('1.1.1.1', 80), 2)"`)
	if r.ExitCode == 0 {
		t.Error("a connection to the internet succeeded inside the sandbox")
	}
}

func TestSandboxHasItsOwnProcessAndHostname(t *testing.T) {
	needSandbox(t)
	r := simulate(t, "echo $$; hostname")
	lines := strings.Fields(r.Stdout)
	if len(lines) < 2 || lines[1] != "safeshell" {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if lines[0] == "" || len(lines[0]) > 3 {
		t.Errorf("PID %q does not look like a fresh PID namespace", lines[0])
	}
}

func TestOutputIsCapped(t *testing.T) {
	needSandbox(t)
	r := simulate(t, "head -c 500000 /dev/zero | tr '\\0' x")
	if !r.Truncated || len(r.Stdout) > 70<<10 {
		t.Errorf("truncated=%v len=%d", r.Truncated, len(r.Stdout))
	}
}

func TestNoScratchDirectoriesLeakAndRunsDoNotInterfere(t *testing.T) {
	needSandbox(t)
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	before, _ := filepath.Glob(filepath.Join(base, "ss-sim-*"))

	var wg sync.WaitGroup
	errs := make(chan string, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d := fixture(t)
			r := simulate(t, "--cwd", d, "rm -rf proj; echo n > only-"+string(rune('a'+i)))
			if _, ok := r.find("/proj"); !ok {
				errs <- "missing proj delete"
			}
			for j := 0; j < 4; j++ {
				if j != i {
					if _, ok := r.find("/only-" + string(rune('a'+j))); ok {
						errs <- "saw another run's file"
					}
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	after, _ := filepath.Glob(filepath.Join(base, "ss-sim-*"))
	if len(after) > len(before) {
		t.Errorf("scratch directories leaked: %v", after)
	}
}

func TestRelativePathsUseTheGivenDirectory(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	r := simulate(t, "--cwd", d, "pwd; cat keep.txt")
	if !strings.Contains(r.Stdout, d) || !strings.Contains(r.Stdout, "keep") {
		t.Errorf("stdout = %q", r.Stdout)
	}
}

type analysis struct {
	Level    string
	Decision string
	Effects  []struct {
		Kind     string
		Evidence string
		Summary  string
		Details  []string
	}
	Verdict struct{ Level string }
	Sim     struct{ Warnings []struct{ Writable []string } }
}

func analyze(t *testing.T, args ...string) analysis {
	t.Helper()
	out, err := exec.Command(ssBin, append([]string{"analyze", "--json"}, args...)...).Output()
	if _, ok := err.(*exec.ExitError); err != nil && !ok { // non-zero exit codes are expected for blocks
		t.Fatalf("safeshell analyze %v: %v", args, err)
	}
	var a analysis
	if err := json.Unmarshal(out, &a); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	return a
}

func (a analysis) effect(kind string) (struct {
	Kind     string
	Evidence string
	Summary  string
	Details  []string
}, bool) {
	for _, e := range a.Effects {
		if e.Kind == kind {
			return e, true
		}
	}
	return e0, false
}

var e0 struct {
	Kind     string
	Evidence string
	Summary  string
	Details  []string
}

func TestAnalyzeObservesAndBlocksARecursiveDelete(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	a := analyze(t, "--cwd", d, "rm -rf proj")
	fs, ok := a.effect("files")
	if !ok || fs.Evidence != "OBSERVED" || !strings.Contains(fs.Summary, "delete 2 files") {
		t.Fatalf("file effect: %+v", fs)
	}
	if a.Level != "R3" || a.Decision != "block" {
		t.Errorf("level %s decision %s", a.Level, a.Decision)
	}
	if _, err := os.Stat(filepath.Join(d, "proj", "a.txt")); err != nil {
		t.Error("the analysis touched the real files")
	}
}

func TestAnalyzeRaisesTheLevelWhenImportantFilesAreDeleted(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	os.MkdirAll(filepath.Join(d, "repo", ".git"), 0o755)
	os.WriteFile(filepath.Join(d, "repo", ".git", "HEAD"), []byte("ref"), 0o644)
	a := analyze(t, "--cwd", d, "rm repo/.git/HEAD")
	if a.Verdict.Level != "R2" || a.Level != "R3" {
		t.Errorf("rules %s final %s: deleting git history should be raised by what was observed", a.Verdict.Level, a.Level)
	}
}

func TestAnalyzeListsTheRealProcessesAPkillWouldHit(t *testing.T) {
	needSandbox(t)
	marker := "ss-integration-marker-" + time.Now().Format("150405")
	// the marker is $0 of the shell, so it appears in the command line without breaking sleep
	victim := exec.Command("sh", "-c", "sleep 60", marker)
	if err := victim.Start(); err != nil {
		t.Skip("cannot start sh")
	}
	defer func() { victim.Process.Kill(); victim.Wait() }()

	a := analyze(t, "pkill -f "+marker)
	pe, ok := a.effect("processes")
	if !ok || pe.Evidence != "INSPECTED" || !strings.Contains(pe.Summary, "1 running") {
		t.Fatalf("process effect: %+v", pe)
	}
	if err := victim.Process.Signal(syscall.Signal(0)); err != nil {
		t.Errorf("the victim is gone (%v): analysis must never signal anything", err)
	}
}

func TestAnalyzeIdentifiesARealDiskWithoutTouchingIt(t *testing.T) {
	needSandbox(t)
	if _, err := os.Stat("/sys/class/block"); err != nil {
		t.Skip("no /sys/class/block")
	}
	entries, _ := os.ReadDir("/sys/class/block")
	var disk string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "sd") || strings.HasPrefix(e.Name(), "nvme") || strings.HasPrefix(e.Name(), "vd") {
			disk = e.Name()
			break
		}
	}
	if disk == "" {
		t.Skip("no sd/nvme/vd disk on this machine")
	}
	a := analyze(t, "dd if=/dev/zero of=/dev/"+disk+" bs=1M")
	de, ok := a.effect("disk")
	if !ok || de.Evidence != "INSPECTED" || a.Decision != "hard-block" || !strings.Contains(de.Summary, "/dev/"+disk) {
		t.Errorf("%+v decision %s", de, a.Decision)
	}
}

func TestNetworkAttemptsAreRecordedWithTheirDestination(t *testing.T) {
	needSandbox(t)
	r := simulate(t, `python3 -c "import socket; socket.create_connection(('203.0.113.9', 8080), 2)"`)
	for _, n := range r.Net {
		if n.Family == "inet" && n.Addr == "203.0.113.9" && n.Port == 8080 {
			return
		}
	}
	t.Errorf("connection attempt not recorded: %+v", r.Net)
}

func TestHostUnixSocketIsNotReachableFromTheSandbox(t *testing.T) {
	needSandbox(t)
	sock := filepath.Join(t.TempDir(), "host.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan struct{}, 1)
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
			accepted <- struct{}{}
		}
	}()
	r := simulate(t, `python3 -c "import socket; s=socket.socket(socket.AF_UNIX); s.connect('`+sock+`')"`)
	if r.ExitCode == 0 {
		t.Error("the sandbox connected to a socket on the host")
	}
	select {
	case <-accepted:
		t.Error("the host service received a connection from the sandbox")
	case <-time.After(300 * time.Millisecond):
	}
	for _, n := range r.Net {
		if n.Family == "unix" && n.Addr == sock {
			return
		}
	}
	t.Errorf("socket attempt not recorded: %+v", r.Net)
}

func TestAnalyzeReportsNetworkAttempts(t *testing.T) {
	needSandbox(t)
	a := analyze(t, `python3 -c "import socket; socket.create_connection(('203.0.113.9', 8080), 2)"`)
	e, ok := a.effect("network")
	if !ok || e.Evidence != "OBSERVED" || !strings.Contains(strings.Join(e.Details, "\n"), "203.0.113.9:8080") {
		t.Errorf("network effect: %+v", e)
	}
}

func TestACleanCommandRecordsNoNetworkAttempts(t *testing.T) {
	needSandbox(t)
	if r := simulate(t, `echo hi > /tmp/ss-net-test.txt`); len(r.Net) != 0 {
		t.Errorf("unexpected attempts: %+v", r.Net)
	}
}

func TestResourceLimitsApplyToTheCommand(t *testing.T) {
	needSandbox(t)
	r := simulate(t, `ulimit -f; ulimit -c; dd if=/dev/zero of=/tmp/ss-big bs=1M count=1100 2>&1 | tail -1`)
	if !strings.HasPrefix(r.Stdout, "1048576\n0\n") { // bash counts ulimit -f in KiB
		t.Errorf("limits not applied: %q", r.Stdout)
	}
	if _, err := os.Stat("/tmp/ss-big"); err == nil {
		t.Error("write reached the real /tmp")
	}
}

func TestAnalyzeAsksAptWhatARemovalWouldTake(t *testing.T) {
	needSandbox(t)
	if exec.Command("dpkg", "-s", "gnome-shell").Run() != nil {
		t.Skip("gnome-shell is not installed here")
	}
	a := analyze(t, "apt remove -y gnome-shell")
	e, ok := a.effect("packages")
	if !ok || e.Evidence != "PREVIEWED" || !strings.Contains(e.Summary, "gnome-shell") {
		t.Fatalf("package effect: %+v", e)
	}
	if a.Level != "R4" || a.Decision != "hard-block" {
		t.Errorf("removing the desktop must be a hard block, got %s %s", a.Level, a.Decision)
	}
}

func TestAnalyzeListsWhatDependsOnAServiceBeingStopped(t *testing.T) {
	needSandbox(t)
	a := analyze(t, "systemctl stop dbus")
	e, ok := a.effect("services")
	if !ok || e.Evidence != "INSPECTED" || !strings.Contains(strings.Join(e.Details, "\n"), "message bus") {
		t.Fatalf("service effect: %+v", e)
	}
	if a.Level != "R3" {
		t.Errorf("stopping dbus must be at least irreversible, got %s", a.Level)
	}
}

func TestAnalyzeListsWhatAGitCommandWouldLose(t *testing.T) {
	if _, err := os.Stat(ssBin); err != nil { // the inspection reads git only, so the sandbox is not needed
		t.Skip("bin/safeshell not built (run: make build)")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null") // the binary under test inherits these, so no setting of this machine's leaks in
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-q", "-m", "first")

	a := analyze(t, "--cwd", dir, "git reset --hard")
	if e, ok := a.effect("git"); !ok || e.Evidence != "INSPECTED" || !strings.Contains(e.Summary, "nothing would be lost") {
		t.Fatalf("clean repository: %+v", e)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a = analyze(t, "--cwd", dir, "git reset --hard")
	e, ok := a.effect("git")
	if !ok || e.Evidence != "INSPECTED" || !strings.Contains(strings.Join(e.Details, "\n"), "changed a.txt") {
		t.Fatalf("edited file: %+v", e)
	}
	if a.Level != "R3" {
		t.Errorf("level %s", a.Level)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(b) != "edited\n" {
		t.Error("the preview changed the real file")
	}
}

func TestSandboxNotesOnlyConcernWhatTheCommandTouches(t *testing.T) {
	needSandbox(t)
	d := fixture(t)
	analyze := func(line string) string {
		out, err := exec.Command(ssBin, "analyze", "--cwd", d, line).CombinedOutput()
		var ee *exec.ExitError
		if err != nil && !(errors.As(err, &ee) && (ee.ExitCode() == 3 || ee.ExitCode() == 4)) { // 3 and 4 are R3/R4 verdicts, not failures
			t.Fatalf("safeshell analyze %q: %v\n%s", line, err, out)
		}
		return string(out)
	}
	if out := analyze(`find . -name "*.log" -delete`); strings.Contains(out, "/var/lib/docker") || strings.Contains(out, "/boot") {
		t.Errorf("notes about untouched folders were shown:\n%s", out)
	}
	// The docker note only exists where /var/lib/docker cannot be read, so it is not checked on every machine.
	if _, err := os.ReadDir("/var/lib/docker"); err != nil {
		if out := analyze("rm -rf /var/lib/docker/x"); !strings.Contains(out, "Sandbox note: /var/lib/docker") {
			t.Errorf("the note about the folder being touched is missing:\n%s", out)
		}
	}
}

func TestAnUnknownScriptIsStillAllowedWhenTheGapsDoNotMatter(t *testing.T) {
	needSandbox(t)
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "noop.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := analyze(t, "--cwd", d, "./noop.sh")
	if len(a.Sim.Warnings) == 0 {
		t.Skip("the sandbox mirrors everything here, so there is no gap to ignore")
	}
	for _, w := range a.Sim.Warnings {
		if len(w.Writable) > 0 {
			t.Skip("a gap here is writable, so the script is rightly not allowed unattended")
		}
	}
	if a.Decision != "allow" || a.Level != "R1" {
		t.Errorf("decision %s level %s", a.Decision, a.Level)
	}
}
