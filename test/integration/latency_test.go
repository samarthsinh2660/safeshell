package integration

import (
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"
)

// guardP95Limit is the most a p95 `safeshell guard` of a safe line may take, process start
// to exit. It is generous on purpose (shared CI machines are slow): this catches a check
// that got slow, not noise. The architecture target is 20 ms.
const guardP95Limit = 50 * time.Millisecond

var safeLines = strings.Split(`ls
ls -la
pwd
echo hello
echo $HOME
cat /etc/hostname
whoami
date
uname -a
id
df -h
du -sh .
ps aux
head -n 5 /etc/passwd
tail -n 5 /etc/passwd
wc -l /etc/passwd
grep root /etc/passwd
find . -name '*.go'
which ls
env
hostname
uptime
free -m
sort /etc/hosts
cd /tmp
cd ..
git status
git log --oneline
git diff
ls /usr/bin | head`, "\n")

// The command line a person's Enter runs is `safeshell guard -- <line>`. This times it as a
// new process, the way the shell hook starts it; without a daemon, which is the slowest
// of the two ways.
func TestGuardLatencyForSafeLines(t *testing.T) {
	needSandbox(t)
	home := t.TempDir()
	run := t.TempDir()
	if err := os.Chmod(run, 0o700); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "HOME="+home, "XDG_STATE_HOME="+home+"/state", "XDG_DATA_HOME="+home+"/data",
		"XDG_CONFIG_HOME="+home+"/config", "XDG_RUNTIME_DIR="+run, "SAFESHELL_NODAEMON=1")
	var took []time.Duration
	for _, line := range safeLines {
		cmd := exec.Command(ssBin, "guard", "--", line)
		cmd.Env, cmd.Dir = env, home
		start := time.Now()
		out, err := cmd.CombinedOutput()
		d := time.Since(start)
		if err != nil {
			t.Fatalf("guard %q: %v\n%s", line, err, out)
		}
		took = append(took, d)
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	p95 := took[len(took)*95/100]
	t.Logf("guard latency over %d safe lines: p50 %v, p95 %v, max %v (limit %v)",
		len(took), took[len(took)/2], p95, took[len(took)-1], guardP95Limit)
	if p95 > guardP95Limit {
		t.Errorf("guard p95 is %v, over the %v limit: the check on every Enter got slow", p95, guardP95Limit)
	}
}
