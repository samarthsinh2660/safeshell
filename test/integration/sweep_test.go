package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/eval"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
)

// snapshotTree lists every file under root with its size and mtime.
func snapshotTree(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		fi, e := d.Info()
		if e == nil {
			out = append(out, p+"|"+fi.Mode().String()+"|"+time.Unix(0, fi.ModTime().UnixNano()).Format(time.RFC3339Nano)+"|"+string(rune(fi.Size()%1000+48)))
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// TestSweepEveryLabelledCommandThroughTheRealBinary runs all ~490 labelled
// commands through `safeshell analyze` against a throwaway HOME. It looks for crashes,
// hangs, malformed output, leaked sandbox directories and, above all, any change
// to the files outside the sandbox. Enable with SS_SWEEP=1 (it takes minutes).
func TestSweepEveryLabelledCommandThroughTheRealBinary(t *testing.T) {
	if os.Getenv("SS_SWEEP") == "" {
		t.Skip("set SS_SWEEP=1 to run the sweep")
	}
	needSandbox(t)
	dev, _ := eval.LoadCorpus()
	held, _ := eval.LoadHeldout()
	rows := append(dev, held...)

	home := t.TempDir()
	for _, f := range []string{"Documents/report.pdf", "projects/app/main.go", "projects/app/.git/HEAD", "notes.txt", ".bashrc", "data/db.sqlite"} {
		p := filepath.Join(home, f)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		_ = os.WriteFile(p, []byte("precious "+f), 0o644)
	}
	before := snapshotTree(home)
	scratchBefore := scratchDirs()

	type problem struct{ cmd, what string }
	var mu sync.Mutex
	var problems []problem
	slowest := time.Duration(0)
	var sem = make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, r := range rows {
		r := r
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			start := time.Now()
			cmd := exec.CommandContext(ctx, ssBin, "analyze", "--json", "--cwd", home, r.Command)
			cmd.Env = append(os.Environ(), "HOME="+home, "SAFESHELL_AI=off", "SAFESHELL_NODAEMON=1", "XDG_STATE_HOME="+home+"/.state", "XDG_DATA_HOME="+home+"/.data")
			out, err := cmd.Output()
			el := time.Since(start)
			mu.Lock()
			defer mu.Unlock()
			if el > slowest {
				slowest = el
			}
			add := func(w string) { problems = append(problems, problem{r.Command, w}) }
			code := 0
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else if err != nil {
				add("did not run: " + err.Error())
				return
			}
			if code != 0 && code != 3 && code != 4 {
				add("unexpected exit code " + string(rune('0'+code%10)))
			}
			var a engine.Analysis
			if json.Unmarshal(out, &a) != nil {
				add("output is not valid JSON")
				return
			}
			if a.Level < a.Verdict.Level {
				add("final level is lower than the rules' level")
			}
			// A delete of the whole filesystem, or a listener such as `nc -l`, legitimately hits the
			// sandbox time limit; that is fine as long as it is reported and never runs unattended.
			if a.Sim != nil && a.Sim.TimedOut && (a.SimNote == "" || a.Decision == domain.Allow) {
				add("simulation timed out without being flagged")
			}
			if el > 45*time.Second {
				add("took " + el.String())
			}
		}()
	}
	wg.Wait()

	for _, p := range problems {
		t.Errorf("%-60s %s", p.cmd, p.what)
	}
	if after := snapshotTree(home); strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("FILES OUTSIDE THE SANDBOX CHANGED:\n before %d entries, after %d", len(before), len(after))
	}
	if leaked := newScratch(scratchBefore); len(leaked) > 0 {
		t.Errorf("sandbox scratch directories were left behind: %v", leaked)
	}
	t.Logf("%d commands analysed, slowest %v, %d problems", len(rows), slowest, len(problems))
}

func scratchDirs() map[string]bool {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	m := map[string]bool{}
	es, _ := os.ReadDir(base)
	for _, e := range es {
		if strings.HasPrefix(e.Name(), "ss-sim-") {
			m[e.Name()] = true
		}
	}
	return m
}

func newScratch(before map[string]bool) []string {
	var out []string
	for n := range scratchDirs() {
		if !before[n] {
			out = append(out, n)
		}
	}
	return out
}
