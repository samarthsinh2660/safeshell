package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
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
	old := auditPath
	auditPath = func() string { return p }
	t.Cleanup(func() { auditPath = old })
	return p
}

type errWriter struct{}

func (errWriter) Write(p []byte) (n int, err error) {
	return 0, errors.New("write failure")
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

func TestHistoryJSONPrintsRawEntriesAndHonoursLimit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	useHistoryLog(t,
		audit.Entry{Time: now, Line: "ls", Level: "R0", Decision: "allow", Outcome: "ran"},
		audit.Entry{Time: now.Add(time.Second), Line: "rm a", Level: "R2", Decision: "ask", Outcome: "cancelled"},
		audit.Entry{Time: now.Add(2 * time.Second), Line: "rm b", Level: "R2", Decision: "ask", Outcome: "cancelled"},
	)

	code, out, errOut := runSS("history", "--json", "-n", "2")
	if code != 0 || errOut != "" {
		t.Fatalf("code %d stderr %q", code, errOut)
	}
	var got []audit.Entry
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if len(got) != 2 || got[0].Line != "rm b" || got[1].Line != "rm a" {
		t.Errorf("entries %+v", got)
	}
	if !got[0].Time.Equal(now.Add(2 * time.Second)) {
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

func TestHistoryReportsSkippedLines(t *testing.T) {
	p := useHistoryLog(t, audit.Entry{Time: time.Now(), Line: "ls", Level: "R0", Decision: "allow", Outcome: "ran"})
	// Corrupt a line
	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{corrupted json\n")
	f.Close()

	code, out, errOut := runSS("history")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "ls") {
		t.Errorf("expected table output")
	}
	if !strings.Contains(errOut, "1 line in the log could not be read and were skipped.") {
		t.Errorf("expected skipped warning on stderr, got %q", errOut)
	}

	// Also check --json with skipped
	code, _, errOutJSON := runSS("history", "--json")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(errOutJSON, "1 line in the log could not be read and were skipped.") {
		t.Errorf("expected skipped warning on stderr for json, got %q", errOutJSON)
	}
}

func TestHistoryEmptyLogWithSkipped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "corrupt_only.jsonl")
	if err := os.WriteFile(p, []byte("{broken json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := auditPath
	auditPath = func() string { return p }
	defer func() { auditPath = old }()

	code, out, errOut := runSS("history")
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if !strings.Contains(out, "No decisions recorded yet.") {
		t.Errorf("expected no decisions message, got %q", out)
	}
	if !strings.Contains(errOut, "1 line in the log could not be read and were skipped.") {
		t.Errorf("expected skipped warning, got %q", errOut)
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

func TestHistoryReadError(t *testing.T) {
	dir := t.TempDir()
	old := auditPath
	auditPath = func() string { return dir } // dir cannot be read as a file
	defer func() { auditPath = old }()

	code, _, errOut := runSS("history")
	if code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if !strings.Contains(errOut, "safeshell history: could not read") {
		t.Errorf("expected read error message, got %q", errOut)
	}
}

func TestHistoryJSONEncodeError(t *testing.T) {
	useHistoryLog(t, audit.Entry{Time: time.Now(), Line: "ls", Level: "R0", Decision: "allow", Outcome: "ran"})
	var errBuf bytes.Buffer
	code := runHistory([]string{"--json"}, errWriter{}, &errBuf)
	if code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
	if !strings.Contains(errBuf.String(), "could not encode JSON") {
		t.Errorf("expected JSON error, got %q", errBuf.String())
	}
}

func TestOneLineFlattensAndTruncatesByRunes(t *testing.T) {
	if got := oneLine("rm -rf\n  build"); got != "rm -rf build" {
		t.Errorf("got %q", got)
	}
	long := oneLine(strings.Repeat("x", historyLineWidth+10))
	if len([]rune(long)) != historyLineWidth || !strings.HasSuffix(long, "...") {
		t.Errorf("got %q (%d runes)", long, len([]rune(long)))
	}

	// Multi-byte Hindi / Emoji rune handling
	hindi := "नमस्ते " + strings.Repeat("क", 60)
	cut := oneLine(hindi)
	if len([]rune(cut)) != historyLineWidth || !strings.HasSuffix(cut, "...") {
		t.Errorf("got %q (%d runes)", cut, len([]rune(cut)))
	}

	// Control characters replacement
	escaped := oneLine("git commit\x1b[31m -m 'hello\x07'")
	if strings.Contains(escaped, "\x1b") || strings.Contains(escaped, "\x07") {
		t.Errorf("control character was not escaped: %q", escaped)
	}
	if !strings.Contains(escaped, `\x1b`) || !strings.Contains(escaped, `\a`) {
		t.Errorf("expected visible escapes in: %q", escaped)
	}
}
