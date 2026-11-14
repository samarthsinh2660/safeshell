package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func fakeAnalyze(t *testing.T, a engine.Analysis) *[2]string {
	t.Helper()
	old := analyze
	t.Cleanup(func() { analyze = old })
	var got [2]string
	analyze = func(_ context.Context, line, cwd string) engine.Analysis {
		got = [2]string{line, cwd}
		return a
	}
	return &got
}

func sampleAnalysis() engine.Analysis {
	return engine.Analysis{
		Line: "rm -rf ~/projects",
		Verdict: domain.Verdict{
			Level:        domain.R3,
			Findings:     []domain.Finding{{Level: domain.R3, Reason: "Recursively and permanently deletes whole folders."}},
			Alternatives: []domain.Alternative{{Command: "trash-put ~/projects", Note: "moves them to the Trash"}},
		},
		Effects: []domain.Effect{
			{Kind: domain.EffectFS, Evidence: domain.Observed, Summary: "would delete 4,312 files (2.0 GiB)", Details: []string{"deleted /home/me/projects/ (4,312 files)"}, MinLevel: domain.R3},
		},
		Level: domain.R3, Decision: domain.Block,
	}
}

func TestAnalyzeReport(t *testing.T) {
	got := fakeAnalyze(t, sampleAnalysis())
	code, out, _ := runSS("analyze", "--cwd", "/work", "rm", "-rf", "~/projects")
	if code != 3 {
		t.Errorf("a blocked command exits 3, got %d", code)
	}
	if got[0] != "rm -rf ~/projects" || got[1] != "/work" {
		t.Errorf("engine called with %v", got)
	}
	for _, want := range []string{
		"R3 Irreversible  BLOCK (you can override)", "Why", "OBSERVED  files: would delete 4,312 files",
		"deleted /home/me/projects/ (4,312 files)", "Safer options", "trash-put ~/projects",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
}

func TestAnalyzeSaysWhenEffectsRaisedTheLevel(t *testing.T) {
	a := sampleAnalysis()
	a.Verdict.Level, a.Level, a.Decision = domain.R2, domain.R4, domain.HardBlock
	fakeAnalyze(t, a)
	code, out, _ := runSS("analyze", "x")
	if code != 4 || !strings.Contains(out, "looked like R2; what it would really do raised it to R4") {
		t.Errorf("exit %d\n%s", code, out)
	}
}

func TestAnalyzeAllowAndConfirmExitZero(t *testing.T) {
	for _, d := range []domain.Decision{domain.Allow, domain.Confirm} {
		a := engine.Analysis{Line: "ls", Level: domain.R0, Decision: d}
		fakeAnalyze(t, a)
		if code, _, _ := runSS("analyze", "ls"); code != 0 {
			t.Errorf("%s: exit %d", d, code)
		}
	}
}

func TestAnalyzeNotesAndJSON(t *testing.T) {
	a := sampleAnalysis()
	a.SimNote = "no sandbox available on this machine"
	a.Sim = &sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "/boot is partial"}}}
	fakeAnalyze(t, a)
	_, out, _ := runSS("analyze", "--cwd", "/boot", "x")
	if !strings.Contains(out, "no sandbox available") || !strings.Contains(out, "Sandbox note: /boot is partial") {
		t.Errorf("notes missing:\n%s", out)
	}
	_, out, _ = runSS("analyze", "--cwd", "/tmp", "x")
	if strings.Contains(out, "/boot is partial") || !strings.Contains(out, "1 sandbox note about other folders was hidden; add --verbose to see it.") {
		t.Errorf("unrelated note should be hidden with a hint:\n%s", out)
	}
	_, out, _ = runSS("analyze", "--verbose", "--cwd", "/tmp", "x")
	if !strings.Contains(out, "Sandbox note: /boot is partial") || strings.Contains(out, "were hidden") {
		t.Errorf("--verbose should show everything:\n%s", out)
	}
	code, out, _ := runSS("analyze", "--json", "x")
	var j struct {
		Level    string
		Decision string
		Effects  []struct{ Evidence, Kind string }
	}
	if code != 3 || json.Unmarshal([]byte(out), &j) != nil || j.Level != "R3" || j.Decision != "block" || j.Effects[0].Evidence != "OBSERVED" {
		t.Errorf("json: %d %s", code, out)
	}
}

func TestAnalyzeUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"analyze"}, {"analyze", "--cwd"}, {"analyze", "--bogus", "x"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestSandboxNotesFollowSymlinks(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "real")
	link := filepath.Join(d, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	ws := []sandbox.Warning{{Paths: []string{real}, Text: "real"}, {Paths: []string{"/boot"}, Text: "boot"}}
	shown, hidden := sandboxNotes(ws, "rm -rf "+link, "/work", nil, false)
	if len(shown) != 1 || shown[0].Text != "real" || hidden != 1 {
		t.Errorf("through a symlink: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf "+link, "/work", nil, true)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("verbose: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf $X", "/work", nil, false)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("uncertain: shown %v, hidden %d", shown, hidden)
	}
}

func TestHiddenNotesWording(t *testing.T) {
	for n, want := range map[int]string{
		0: "",
		1: "1 sandbox note about other folders was hidden; add --verbose to see it.\n",
		3: "3 sandbox notes about other folders were hidden; add --verbose to see them.\n",
	} {
		var b bytes.Buffer
		hiddenNotes(&b, n, "", "")
		if b.String() != want {
			t.Errorf("%d: got %q", n, b.String())
		}
	}
}

func TestSimulateVerboseShowsEveryNote(t *testing.T) {
	fakeSim(t, sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "boot note"}}}, nil)
	_, out, _ := runSS("simulate", "--cwd", "/work", "rm", "/work/x")
	if strings.Contains(out, "boot note") || !strings.Contains(out, "1 sandbox note about other folders was hidden") {
		t.Errorf("filtered:\n%s", out)
	}
	_, out, _ = runSS("simulate", "--verbose", "--cwd", "/work", "rm", "/work/x")
	if !strings.Contains(out, "Warning: boot note") || strings.Contains(out, "hidden") {
		t.Errorf("verbose:\n%s", out)
	}
}

func TestAnalyzeAsksDockerAndKubectlWhatWouldBeRemoved(t *testing.T) {
	old := toolRunner
	t.Cleanup(func() { toolRunner = old })
	var gotEnv []string
	var gotDir string
	toolRunner = func(_ context.Context, env []string, dir, name string, args ...string) (string, error) {
		gotEnv, gotDir = env, dir
		if name == "docker" {
			return "/web running nginx\n", nil
		}
		return "pod/a\n", nil
	}
	for line, want := range map[string]string{"docker rm web": "docker rm would remove 1 container", "kubectl delete pod a": "kubectl delete would remove 1 object"} {
		dir := t.TempDir()
		a := analyze(engine.WithEnv(context.Background(), []string{"DOCKER_HOST=tcp://theirs", "KUBECONFIG=/theirs"}), line, dir)
		found := false
		for _, e := range a.Effects {
			found = found || (e.Kind == domain.EffectContainers && strings.Contains(e.Summary, want))
		}
		if !found || !envHas(gotEnv, "DOCKER_HOST") || !envHas(gotEnv, "KUBECONFIG") || gotDir != dir {
			t.Errorf("%s: env %v, dir %q, effects %+v", line, gotEnv, gotDir, a.Effects)
		}
	}
}

func TestRealToolRunnerRunsInTheCallersFolderWithTheCallersPath(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mytool"), []byte("#!/bin/sh\npwd\necho $KUBECONFIG $LC_ALL\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + bin, "KUBECONFIG=/theirs"}
	dir := t.TempDir()
	out, err := realToolRunner(context.Background(), env, dir, "mytool")
	if err != nil || strings.TrimSpace(out) != dir+"\n/theirs C" || len(env) != 2 {
		t.Errorf("%q %v", out, err)
	}
}

func TestRealGitRunnerRunsInTheGivenFolderWithACleanEnvironment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_PAGER=evil-pager", "GIT_EXTERNAL_DIFF=evil-diff", "PAGER=evil-pager")
	if out, err := realGitRunner(context.Background(), env, dir, "--version"); err != nil || !strings.HasPrefix(out, "git version") {
		t.Errorf("%q %v", out, err)
	}
	if out, err := realGitRunner(context.Background(), env, dir, "var", "GIT_PAGER"); err != nil || strings.Contains(out, "evil") {
		t.Errorf("the pager from the environment was kept: %q %v", out, err)
	}
	if _, err := realGitRunner(context.Background(), env, dir, "rev-parse", "--git-dir"); err == nil {
		t.Error("an empty folder is not a repository")
	}
}

func TestRunLimitedStopsAtTheTimeoutEvenWhenAGrandchildKeepsTheOutputOpen(t *testing.T) {
	start := time.Now()
	_, err := runLimited(context.Background(), time.Second, os.Environ(), "", "sh", "-c", "sleep 6 & sleep 30")
	if err == nil || time.Since(start) > 3*time.Second {
		t.Errorf("returned after %v with %v", time.Since(start), err)
	}
	dir := t.TempDir()
	if out, err := runLimited(context.Background(), time.Second, []string{"X=seen", "PATH=/bin:/usr/bin"}, dir, "sh", "-c", "pwd; echo $X"); err != nil || !strings.Contains(out, dir) || !strings.Contains(out, "seen") {
		t.Errorf("%q %v", out, err)
	}
}

func TestRunLimitedKillsWhatTheProgramLeavesBehindAndHonoursTheRequest(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	// The parent exits at once; its child keeps running and holding the pipe.
	script := "sleep 30 >/dev/null 2>&1 & echo $! > " + pidFile
	if _, err := runLimited(context.Background(), 5*time.Second, os.Environ(), "", "sh", "-c", script); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	gone := false
	for i := 0; i < 50 && !gone; i++ {
		time.Sleep(20 * time.Millisecond)
		gone = syscall.Kill(pid, 0) != nil
	}
	if !gone {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Error("a descendant outlived the query")
	}
	if _, err := runLimited(context.Background(), time.Second, []string{"PATH=/nonexistent"}, "", "sh"); err == nil {
		t.Error("a program that is not in the requester's PATH must not run")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runLimited(ctx, 5*time.Second, os.Environ(), "", "sleep", "5"); err == nil {
		t.Error("a request that has ended must not start a query")
	}
}

func TestLookPathUsesThePathOfTheRequestersShell(t *testing.T) {
	bin := t.TempDir()
	tool := filepath.Join(bin, "git")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\npwd\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if p, err := lookPath([]string{"PATH=relative::" + bin}, "git"); err != nil || p != tool {
		t.Errorf("%q %v", p, err)
	}
	for _, env := range [][]string{{"PATH=" + bin}, {"PATH=/nonexistent"}, {}} {
		if _, err := lookPath(env, "plain"); err == nil {
			t.Errorf("%v: a file that is not executable was found", env)
		}
	}
	if p, err := lookPath(nil, "/bin/sh"); err != nil || p != "/bin/sh" {
		t.Errorf("%q %v", p, err)
	}
	dir := t.TempDir()
	out, err := realGitRunner(context.Background(), []string{"PATH=" + bin}, dir)
	if err != nil || strings.TrimSpace(out) != dir {
		t.Errorf("must run the client's program in the client's folder: %q %v", out, err)
	}
}

func TestCallerEnvIsTheRequestersWhenThereIsOne(t *testing.T) {
	if env := callerEnv(context.Background()); len(env) != len(os.Environ()) {
		t.Errorf("no request environment should mean ours: %v", env)
	}
	ctx := engine.WithEnv(context.Background(), []string{"KUBECONFIG=/theirs", "GIT_DIR=/x"})
	env := callerEnv(ctx)
	if len(env) != 2 || !envHas(env, "KUBECONFIG") || !envHas(env, "NOPE", "GIT_DIR") || envHas(env, "GIT", "KUBE") {
		t.Errorf("%v", env)
	}
}

func TestAnalyzeAsksTheRepositoryWhatWouldBeLost(t *testing.T) {
	old := gitRunner
	t.Cleanup(func() { gitRunner = old })
	var gotDir string
	var gotEnv []string
	gitRunner = func(_ context.Context, env []string, dir string, args ...string) (string, error) {
		gotDir, gotEnv = dir, env
		if strings.Contains(strings.Join(args, " "), " status ") {
			return " M a.txt\x00", nil
		}
		return "", nil
	}
	dir := t.TempDir()
	reset := func(ctx context.Context) (found bool, a engine.Analysis) {
		a = analyze(ctx, "git reset --hard", dir)
		for _, e := range a.Effects {
			found = found || (e.Kind == domain.EffectGit && strings.Contains(e.Summary, "discard 1 changed file(s)"))
		}
		return found, a
	}
	if found, a := reset(engine.WithEnv(context.Background(), []string{"KUBECONFIG=/theirs"})); !found || gotDir != dir || !envHas(gotEnv, "KUBECONFIG") {
		t.Errorf("dir %q env %v effects %+v", gotDir, gotEnv, a.Effects)
	}
	gotDir = ""
	if found, _ := reset(engine.WithEnv(context.Background(), []string{"GIT_DIR=/elsewhere"})); found || gotDir != "" {
		t.Errorf("a repository chosen by the environment must not be looked at (asked %q)", gotDir)
	}
}
