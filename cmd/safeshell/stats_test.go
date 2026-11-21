package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// useLog makes stats read the fixture log.
func useLog(t *testing.T, path string) {
	t.Helper()
	old := auditPath
	t.Cleanup(func() { auditPath = old })
	auditPath = func() string { return path }
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestStatsTable(t *testing.T) {
	useLog(t, "testdata/stats_audit.jsonl")
	code, out, errOut := runSS("stats")
	want := strings.ReplaceAll(fixture(t, "stats.txt"), "@LOG@", "testdata/stats_audit.jsonl")
	if code != 0 || out != want {
		t.Errorf("exit %d, output:\n%s\nwant:\n%s", code, out, want)
	}
	if !strings.Contains(errOut, "1 line in the log could not be read") {
		t.Errorf("the damaged line is not mentioned: %q", errOut)
	}
}

func TestStatsPrometheusFileIsExact(t *testing.T) {
	useLog(t, "testdata/stats_audit.jsonl")
	f := filepath.Join(t.TempDir(), "safeshell.prom")
	code, out, _ := runSS("stats", "--prometheus", f)
	if code != 0 || out != "" {
		t.Fatalf("exit %d, output %q", code, out)
	}
	b, err := os.ReadFile(f)
	if err != nil || string(b) != fixture(t, "stats.prom") {
		t.Errorf("%v, got:\n%s", err, b)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o644 {
		t.Errorf("the file must be readable by node_exporter: %v", fi.Mode())
	}
	left, _ := filepath.Glob(f + ".*")
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestStatsPrometheusReplacesTheOldFileInOneStep(t *testing.T) {
	useLog(t, "testdata/stats_audit.jsonl")
	f := filepath.Join(t.TempDir(), "safeshell.prom")
	if err := os.WriteFile(f, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := runSS("stats", "--prometheus", f); code != 0 {
		t.Fatal(code)
	}
	if b, _ := os.ReadFile(f); string(b) != fixture(t, "stats.prom") {
		t.Errorf("%s", b)
	}
}

func TestStatsPrometheusOutputPassesPromtool(t *testing.T) {
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Skip("promtool is not installed")
	}
	f := filepath.Join(t.TempDir(), "safeshell.prom")
	if err := os.WriteFile(f, []byte(fixture(t, "stats.prom")), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(promtool, "check", "metrics")
	in, _ := os.Open(f)
	defer in.Close()
	cmd.Stdin = in
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("promtool: %v\n%s", err, out)
	}
}

func TestStatsWithNoLogShowsZeros(t *testing.T) {
	useLog(t, filepath.Join(t.TempDir(), "none.jsonl"))
	code, out, _ := runSS("stats")
	if code != 0 || !strings.Contains(out, "Commands checked             0") {
		t.Errorf("%d %q", code, out)
	}
}

func TestStatsErrors(t *testing.T) {
	useLog(t, "testdata/stats_audit.jsonl")
	for _, args := range [][]string{{"stats", "--prometheus"}, {"stats", "--json"}, {"stats", "--prometheus", "a", "b"}} {
		if code, _, errOut := runSS(args...); code != 2 || !strings.Contains(errOut, "safeshell stats --prometheus FILE") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
	}
	dir := t.TempDir()
	useLog(t, dir) // a folder cannot be read as a log
	if code, _, errOut := runSS("stats"); code != 1 || !strings.Contains(errOut, "could not read") {
		t.Errorf("%d %q", code, errOut)
	}
	useLog(t, "testdata/stats_audit.jsonl")
	if code, _, errOut := runSS("stats", "--prometheus", filepath.Join(dir, "missing", "x.prom")); code != 1 || !strings.Contains(errOut, "Check that the folder exists") {
		t.Errorf("%d %q", code, errOut)
	}
	if code, _, _ := runSS("stats", "--prometheus", dir); code != 1 { // renaming a file over a folder fails
		t.Errorf("%d", code)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}
