package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
	"github.com/samarthsinh2660/safeshell/internal/undo"
)

// answerShared feeds one stream of answers to every reader of the terminal, in order.
func answerShared(t *testing.T, s string) {
	t.Helper()
	old := guardInput
	t.Cleanup(func() { guardInput = old })
	r := strings.NewReader(s)
	guardInput = func() io.Reader { return r }
}

// planRuns records the commands undo ran, and makes them succeed or fail.
func planRuns(t *testing.T, err error) *[]string {
	t.Helper()
	old := runCommand
	t.Cleanup(func() { runCommand = old })
	var ran []string
	runCommand = func(cmd, _ string, _, _ io.Writer) error {
		ran = append(ran, cmd)
		return err
	}
	return &ran
}

func units(t *testing.T, answers map[string]string) {
	t.Helper()
	old := unitRunner
	t.Cleanup(func() { unitRunner = old })
	unitRunner = func(_ context.Context, name string, args ...string) string {
		if name != "systemctl" {
			t.Errorf("ran %s", name)
		}
		return answers[args[0]]
	}
}

func guardAllowed(t *testing.T, line string) string {
	t.Helper()
	a := sampleAnalysis()
	a.Line, a.Level, a.Decision = line, domain.R1, domain.Allow
	fakeAnalyze(t, a)
	answer(t, "")
	code, _, errOut := runSS("guard", "--", line)
	if code != 0 {
		t.Fatalf("guard %d %q", code, errOut)
	}
	return errOut
}

func TestServiceStateIsRecordedAndUndoRunsThePlanThroughTheGuard(t *testing.T) {
	useStore(t)
	units(t, map[string]string{"is-active": "active", "is-enabled": "enabled"})
	if note := guardAllowed(t, "sudo systemctl stop nginx"); !strings.Contains(note, "noted how to undo") {
		t.Fatalf("no note: %q", note)
	}
	ms, _ := snapshot.List(snapshotDir())
	if len(ms) != 1 || len(ms[0].Plans) != 1 || ms[0].Plans[0].Command != "sudo systemctl start nginx" {
		t.Fatalf("manifest: %+v", ms)
	}

	ran := planRuns(t, nil)
	seen := fakeAnalyze(t, plainAllow("sudo systemctl start nginx"))
	answerShared(t, "yes\ny\n") // run it now, then the guard's own question
	code, out, errOut := runSS("undo")
	if code != 0 || !strings.Contains(out, "sudo systemctl start nginx") || !strings.Contains(out, "nginx was active and enabled before") {
		t.Fatalf("undo: %d %q %q", code, out, errOut)
	}
	if seen[0] != "sudo systemctl start nginx" {
		t.Errorf("the plan did not go through the guard: analysed %q", seen[0])
	}
	if len(*ran) != 1 || (*ran)[0] != "sudo systemctl start nginx" {
		t.Errorf("ran %v", *ran)
	}
	if ms, _ := snapshot.List(snapshotDir()); ms[0].Undone == nil {
		t.Error("not marked undone")
	}
}

func plainAllow(line string) (a engine.Analysis) {
	a = sampleAnalysis()
	a.Line, a.Level, a.Decision = line, domain.R1, domain.Allow
	return a
}

func TestServiceWithUnknownStateRecordsNothing(t *testing.T) {
	useStore(t)
	units(t, nil)
	if note := guardAllowed(t, "systemctl stop nginx"); note != "" && strings.Contains(note, "undo") {
		t.Errorf("note %q", note)
	}
	if ms, _ := snapshot.List(snapshotDir()); len(ms) != 0 {
		t.Errorf("recorded %d", len(ms))
	}
}

func TestServiceAlreadyStoppedHasNothingToRestore(t *testing.T) {
	useStore(t)
	units(t, map[string]string{"is-active": "inactive", "is-enabled": "enabled"})
	guardAllowed(t, "systemctl stop nginx")
	if ms, _ := snapshot.List(snapshotDir()); len(ms) != 0 {
		t.Errorf("recorded %d", len(ms))
	}
}

func TestPlanNeverRunsByItself(t *testing.T) {
	useStore(t)
	units(t, map[string]string{"is-active": "active", "is-enabled": "enabled"})
	guardAllowed(t, "systemctl stop nginx")
	ran := planRuns(t, nil)

	code, out, _ := runSS("undo", "--yes")
	if code != 1 || len(*ran) != 0 || !strings.Contains(out, "does not run this by itself") {
		t.Errorf("--yes: %d %q ran %v", code, out, *ran)
	}
	answer(t, "no\n")
	code, out, _ = runSS("undo")
	if code != 1 || len(*ran) != 0 || !strings.Contains(out, "Nothing was run") {
		t.Errorf("declined: %d %q ran %v", code, out, *ran)
	}
	if ms, _ := snapshot.List(snapshotDir()); ms[0].Undone != nil {
		t.Error("declined plan must not mark the snapshot undone")
	}
	answerShared(t, "yes\nn\n") // the guard's question is declined
	code, _, errOut := runSS("undo")
	if code != 1 || len(*ran) != 0 || !strings.Contains(errOut, "Cancelled") {
		t.Errorf("guard declined: %d %q ran %v", code, errOut, *ran)
	}
}

func TestPlanFailureIsReported(t *testing.T) {
	useStore(t)
	units(t, map[string]string{"is-active": "active", "is-enabled": "enabled"})
	guardAllowed(t, "systemctl stop nginx")
	planRuns(t, errors.New("exit status 1"))
	fakeAnalyze(t, plainAllow("sudo systemctl start nginx"))
	answerShared(t, "yes\ny\n")
	code, _, errOut := runSS("undo")
	if code != 1 || !strings.Contains(errOut, "that command failed") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestRecordFailureIsSaid(t *testing.T) {
	old := snapshotDir
	t.Cleanup(func() { snapshotDir = old })
	a, b := t.TempDir(), t.TempDir()
	calls := 0
	snapshotDir = func() string { // the second look (recording the plans) finds another store
		calls++
		if calls == 2 {
			return b
		}
		return a
	}
	units(t, map[string]string{"is-active": "active", "is-enabled": "enabled"})
	if note := guardAllowed(t, "systemctl stop nginx"); !strings.Contains(note, "could not record how to undo") {
		t.Errorf("note %q", note)
	}
}

// history writes an apt log with one install that started now.
func history(t *testing.T, line, install string) {
	t.Helper()
	old := aptHistoryPath
	t.Cleanup(func() { aptHistoryPath = old })
	aptHistoryPath = filepath.Join(t.TempDir(), "history.log")
	log := "Start-Date: 2000-01-01  00:00:00\nCommandline: " + line + "\nInstall: old:amd64 (1)\nEnd-Date: 2000-01-01  00:00:01\n\n" +
		"Start-Date: " + time.Now().Format("2006-01-02  15:04:05") + "\nCommandline: " + line + "\nInstall: " + install + "\nUpgrade: libssl3:amd64 (1, 2)\nEnd-Date: x\n"
	if err := os.WriteFile(aptHistoryPath, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAptInstallUndoProposesRemovingExactlyWhatItAdded(t *testing.T) {
	useStore(t)
	if note := guardAllowed(t, "sudo apt install -y nginx"); !strings.Contains(note, "noted how to undo") {
		t.Fatalf("no record: %q", note)
	}
	time.Sleep(1100 * time.Millisecond) // apt logs whole seconds, after the guard
	history(t, "apt install -y nginx", "nginx-common:amd64 (1.24, automatic), nginx:amd64 (1.24)")

	ran := planRuns(t, nil)
	seen := fakeAnalyze(t, plainAllow("sudo apt remove nginx-common:amd64 nginx:amd64"))
	answerShared(t, "yes\ny\n")
	code, out, errOut := runSS("undo")
	if code != 0 {
		t.Fatalf("undo: %d %q %q", code, out, errOut)
	}
	for _, want := range []string{"sudo apt remove nginx-common:amd64 nginx:amd64", "1 of them only as dependencies", "upgraded 1", "not reversed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if seen[0] != "sudo apt remove nginx-common:amd64 nginx:amd64" || len(*ran) != 1 {
		t.Errorf("analysed %q ran %v", seen[0], *ran)
	}
}

func TestAptInstallUndoSaysWhenTheLogHasNothing(t *testing.T) {
	useStore(t)
	guardAllowed(t, "apt install nginx")
	ran := planRuns(t, nil)

	old := aptHistoryPath
	t.Cleanup(func() { aptHistoryPath = old })
	aptHistoryPath = filepath.Join(t.TempDir(), "missing.log")
	code, out, _ := runSS("undo")
	if code != 1 || !strings.Contains(out, "could not read apt's log") {
		t.Errorf("missing log: %d %q", code, out)
	}
	history(t, "apt install vim", "vim:amd64 (1)") // a different command
	code, out, _ = runSS("undo")
	if code != 1 || !strings.Contains(out, aptNotFound) || len(*ran) != 0 {
		t.Errorf("no match: %d %q ran %v", code, out, *ran)
	}
}

func TestUndoRestoresFilesThenOffersThePlan(t *testing.T) {
	useStore(t)
	f := filepath.Join(t.TempDir(), "keep.txt")
	os.WriteFile(f, []byte("precious"), 0o644)
	m, err := snapshot.CreateAt(snapshotDir(), snapshot.Origin{}, "systemctl stop nginx", []snapshot.Target{{Path: f}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.AddPlans(snapshotDir(), m.ID, []undo.Plan{{Command: "sudo systemctl start nginx", Explain: "starts it"}})
	os.WriteFile(f, []byte("changed"), 0o644)

	ran := planRuns(t, nil)
	fakeAnalyze(t, plainAllow("sudo systemctl start nginx"))
	answerShared(t, "yes\nyes\ny\n") // restore the files, run the plan, the guard's question
	code, out, errOut := runSS("undo")
	if b, _ := os.ReadFile(f); code != 0 || string(b) != "precious" || len(*ran) != 1 {
		t.Fatalf("%d %q %q ran %v file %q", code, out, errOut, *ran, b)
	}

	answer(t, "no\n") // declining the file restore stops before any plan is offered
	if code, _, _ := runSS("undo", m.ID); code != 1 || len(*ran) != 1 {
		t.Errorf("declined: %d ran %v", code, *ran)
	}
	// With --yes the files are restored and the plan is only shown.
	code, out, _ = runSS("undo", "--yes", m.ID)
	if code != 0 || !strings.Contains(out, "does not run this by itself") || len(*ran) != 1 {
		t.Errorf("--yes: %d %q ran %v", code, out, *ran)
	}
}
