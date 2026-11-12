package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
)

// useHistoryLog points the history command at a temporary log holding entries.
func useHistoryLog(t *testing.T, entries ...audit.Entry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	for _, e := range entries {
		if err := audit.Append(p, e); err != nil {
			t.Fatal(err)
		}
	}
	old := historyPath
	historyPath = func() string { return p }
	t.Cleanup(func() { historyPath = old })
	return p
}

func TestHistoryPrintsNewestFirst(t *testing.T) {
	first := time.Date(2026, 11, 12, 9, 0, 0, 0, time.Local)
	useHistoryLog(t,
		audit.Entry{Time: first, Line: "rm -rf build", Level: "R3", Decision: "block", Outcome: "cancelled"},
		audit.Entry{Time: first.Add(time.Minute), Line: "git push --force", Level: "R3", Decision: "allow", Outcome: "ran"},
	)

	code, out, errOut := runSS("history")
	if code != 0 || errOut != "" {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	push := strings.Index(out, "git push --force")
	rm := strings.Index(out, "rm -rf build")
	if push < 0 || rm < 0 || push > rm {
		t.Errorf("expected the newest entry first:\n%s", out)
	}
	if !strings.Contains(out, "TIME") || !strings.Contains(out, "OUTCOME") {
		t.Errorf("missing table header:\n%s", out)
	}
}

func TestHistoryLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	useHistoryLog(t,
		audit.Entry{Time: now, Line: "ls", Level: "R0", Decision: "allow", Outcome: "ran"},
		audit.Entry{Time: now.Add(time.Second), Line: "rm a", Level: "R2", Decision: "ask", Outcome: "ran"},
		audit.Entry{Time: now.Add(2 * time.Second), Line: "rm b", Level: "R2", Decision: "ask", Outcome: "cancelled"},
	)

	code, out, _ := runSS("history", "-n", "2")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected a header and two rows, got %d lines:\n%s", len(lines), out)
	}
	if !strings.Contains(lines[1], "rm b") || !strings.Contains(lines[2], "rm a") {
		t.Errorf("expected the two newest entries only:\n%s", out)
	}
}

func TestHistoryJSONPrintsRawEntries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	useHistoryLog(t,
		audit.Entry{Time: now, Line: "ls", Level: "R0", Decision: "allow", Outcome: "ran"},
		audit.Entry{Time: now.Add(time.Second), Line: "rm a", Level: "R2", Decision: "ask", Outcome: "cancelled"},
	)

	code, out, errOut := runSS("history", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	var got []audit.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].Line != "rm a" || got[1].Line != "ls" {
		t.Errorf("entries %+v", got)
	}
	if !got[0].Time.Equal(now.Add(time.Second)) {
		t.Errorf("time %v", got[0].Time)
	}
}

func TestHistoryEmptyLog(t *testing.T) {
	useHistoryLog(t)
	code, out, errOut := runSS("history")
	if code != 0 || errOut != "" {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	if !strings.Contains(out, "No decisions recorded yet.") {
		t.Errorf("out %q", out)
	}
}

func TestHistoryRejectsBadLineCount(t *testing.T) {
	useHistoryLog(t)
	for _, args := range [][]string{{"history", "-n"}, {"history", "-n", "0"}, {"history", "-n", "abc"}, {"history", "--wat"}} {
		if code, _, _ := runSS(args...); code != 2 {
			t.Errorf("%v: code %d, want 2", args, code)
		}
	}
}

func TestOneLineFlattensAndTruncates(t *testing.T) {
	if got := oneLine("rm -rf\n  build"); got != "rm -rf build" {
		t.Errorf("got %q", got)
	}
	long := oneLine(strings.Repeat("x", historyLineWidth+10))
	if len(long) != historyLineWidth || !strings.HasSuffix(long, "...") {
		t.Errorf("got %q (%d bytes)", long, len(long))
	}
}
