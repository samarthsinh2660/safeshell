package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

func TestAppendWritesOneJSONLinePerEntryPrivately(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "audit.jsonl")
	for _, l := range []string{"rm a", "rm b"} {
		if err := Append(p, Entry{Time: time.Unix(1, 0).UTC(), Line: l, Level: "R3", Decision: "block", Outcome: "cancelled"}); err != nil {
			t.Fatal(err)
		}
	}
	f, _ := os.Open(p)
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e.Line)
	}
	if len(lines) != 2 || lines[0] != "rm a" || lines[1] != "rm b" {
		t.Errorf("lines %v", lines)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/x")
	if got := DefaultPath(); got != "/x/safeshell/audit.jsonl" {
		t.Error(got)
	}
}

func TestDefaultPathDefaultFallback(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	h, _ := os.UserHomeDir()
	expected := filepath.Join(h, ".local", "state", "safeshell", "audit.jsonl")
	if got := DefaultPath(); got != expected {
		t.Errorf("got %s, want %s", got, expected)
	}
}

func TestAppendFailsCleanlyWhenUnwritable(t *testing.T) {
	if Append("/proc/nope/audit.jsonl", Entry{}) == nil {
		t.Error("expected an error")
	}
}

func TestDefaultPathFallsBackToTheHomeFolder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/alice")
	if got := DefaultPath(); got != "/home/alice/.local/state/safeshell/audit.jsonl" {
		t.Error(got)
	}
}

func TestAppendFailsWhenTheFolderCannotBeMade(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if Append(filepath.Join(file, "sub", "audit.jsonl"), Entry{}) == nil {
		t.Error("expected an error when a parent is a file")
	}
}

func TestOutcome(t *testing.T) {
	for _, c := range []struct {
		name         string
		d            domain.Decision
		ran          bool
		line, cmd    string
		outcome, run string
	}{
		{"cancelled", domain.Block, false, "rm x", "rm x", "cancelled", ""},
		{"cancelled even with a replacement picked", domain.Block, false, "rm x", "trash x", "cancelled", ""},
		{"allowed", domain.Allow, true, "ls", "ls", "allowed", ""},
		{"allowed wins over a different command", domain.Allow, true, "ls", "ls -l", "allowed", ""},
		{"ran as typed", domain.Block, true, "rm x", "rm x", "ran", ""},
		{"replaced", domain.Block, true, "rm x", "trash x", "replaced", "trash x"},
	} {
		outcome, run := Outcome(c.d, c.ran, c.line, c.cmd)
		if outcome != c.outcome || run != c.run {
			t.Errorf("%s: got %q, %q want %q, %q", c.name, outcome, run, c.outcome, c.run)
		}
	}
}

func TestAppendFailsWhenThePathIsAFolder(t *testing.T) {
	if Append(t.TempDir(), Entry{}) == nil {
		t.Error("expected an error when the path is a folder")
	}
}

func TestReadMissingFileReturnsNil(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nonexistent.jsonl")
	entries, skipped, err := Read(p)
	if err != nil || skipped != 0 || entries != nil {
		t.Fatalf("expected nil, 0, nil; got entries=%v, skipped=%d, err=%v", entries, skipped, err)
	}
}

func TestReadSkipsCorruptedLinesAndBlankLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	content := "\n\n{\"time\":\"2026-11-12T09:00:00Z\",\"line\":\"ls\",\"level\":\"R0\",\"decision\":\"allow\",\"outcome\":\"ran\"}\n\n{broken json line\n{\"time\":\"2026-11-12T09:01:00Z\",\"line\":\"pwd\",\"level\":\"R0\",\"decision\":\"allow\",\"outcome\":\"ran\"}\n"
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, skipped, err := Read(p)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if skipped != 1 {
		t.Errorf("expected 1 skipped line, got %d", skipped)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 valid entries, got %d", len(entries))
	}
	if entries[0].Line != "ls" || entries[1].Line != "pwd" {
		t.Errorf("unexpected entries: %+v", entries)
	}
}

func TestReadLineOver64KB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.jsonl")
	bigLine := strings.Repeat("echo 'a';", 10000) // ~100 KB
	e := Entry{
		Time:     time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC),
		Line:     bigLine,
		Level:    "R1",
		Decision: "allow",
		Outcome:  "ran",
	}
	data, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	entries, skipped, err := Read(p)
	if err != nil {
		t.Fatalf("failed to read line >64KB: %v", err)
	}
	if skipped != 0 || len(entries) != 1 {
		t.Fatalf("expected 1 entry, 0 skipped; got len=%d, skipped=%d", len(entries), skipped)
	}
	if entries[0].Line != bigLine {
		t.Errorf("line content mismatch")
	}
}

func TestReadLineExceeding1MBReturnsScannerError(t *testing.T) {
	p := filepath.Join(t.TempDir(), "huge.jsonl")
	hugeLine := strings.Repeat("x", 2*1024*1024)
	if err := os.WriteFile(p, []byte(hugeLine+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err := Read(p)
	if err == nil {
		t.Error("expected scanner error on line >1MB")
	}
}

func TestReadFailsOnUnreadableFile(t *testing.T) {
	dir := t.TempDir()
	_, _, err := Read(dir)
	if err == nil {
		t.Error("expected error reading directory as file")
	}
}

func TestAppendTightensALooseFolder(t *testing.T) {
	d := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Append(filepath.Join(d, "audit.jsonl"), Entry{Line: "ls"}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(d); st.Mode().Perm() != 0o700 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestAppendKeepsOneOldFileWhenTheLogIsTooBig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(p, make([]byte, maxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Append(p, Entry{Line: "new"}); err != nil {
		t.Fatal(err)
	}
	if old, _ := os.Stat(p + ".old"); old == nil || old.Size() != maxBytes {
		t.Error("the full log should have moved to .old")
	}
	if es, _, _ := Read(p); len(es) != 1 || es[0].Line != "new" {
		t.Errorf("entries %v", es)
	}
}

func TestAppendFailsWhenTheLogCannotBeRotated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	if err := os.WriteFile(p, make([]byte, maxBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p+".old", "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := Append(p, Entry{Line: "new"}); err == nil {
		t.Error("rotation onto a folder should fail")
	}
}
