package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
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
