package audit

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRecentKeepsTheLastEntriesInsideTheWindow(t *testing.T) {
	p := filepath.Join(t.TempDir(), "audit.jsonl")
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	for i, ago := range []time.Duration{time.Hour, 20 * time.Minute, 10 * time.Minute, time.Minute} {
		if err := Append(p, Entry{Time: now.Add(-ago), Line: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	got := Recent(p, now, 30*time.Minute, 2)
	if len(got) != 2 || got[0].Line != "c" || got[1].Line != "d" {
		t.Errorf("%+v", got)
	}
	if got := Recent(p, now, 30*time.Minute, 10); len(got) != 3 || got[0].Line != "b" {
		t.Errorf("the hour-old entry is outside the window: %+v", got)
	}
	if got := Recent(filepath.Join(t.TempDir(), "none"), now, time.Hour, 5); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}
