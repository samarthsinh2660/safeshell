package convo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/nl"
)

func store(t *testing.T, now *time.Time) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "conversations"), Now: func() time.Time { return *now }}
}

func TestExchangesComeBackOldestFirstPrivateAndMasked(t *testing.T) {
	now := time.Now()
	s := store(t, &now)
	if err := s.Add("123", nl.Turn{Request: "deploy with TOKEN=abc123secret", Clarifications: []string{"password=hunter2x"}, Command: "make deploy", Ran: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("123", nl.Turn{Request: "what is that", Answer: "A thing."}); err != nil {
		t.Fatal(err)
	}
	got := s.Load("123")
	if len(got) != 2 || got[0].Command != "make deploy" || !got[0].Ran || got[1].Answer != "A thing." {
		t.Fatalf("%+v", got)
	}
	data, _ := os.ReadFile(filepath.Join(s.Dir, "123.json"))
	if strings.Contains(string(data), "abc123secret") || strings.Contains(string(data), "hunter2x") {
		t.Errorf("secrets must be masked on disk: %s", data)
	}
	for p, want := range map[string]os.FileMode{s.Dir: 0o700, filepath.Join(s.Dir, "123.json"): 0o600} {
		if fi, _ := os.Stat(p); fi.Mode().Perm() != want {
			t.Errorf("%s is %v, want %v", p, fi.Mode().Perm(), want)
		}
	}
	if len(s.Load("456")) != 0 {
		t.Error("another terminal must not see these")
	}
}

func TestOnlyTheLastFiveAndUnexpiredExchangesAreKept(t *testing.T) {
	now := time.Now()
	s := store(t, &now)
	for i := 0; i < 7; i++ {
		_ = s.Add("7", nl.Turn{Request: string(rune('a' + i)), Answer: "x"})
	}
	got := s.Load("7")
	if len(got) != MaxTurns || got[0].Request != "c" || got[4].Request != "g" {
		t.Errorf("%+v", got)
	}
	now = now.Add(TTL + time.Minute)
	if got := s.Load("7"); len(got) != 0 {
		t.Errorf("expired exchanges must be forgotten: %+v", got)
	}
	_ = s.Add("7", nl.Turn{Request: "new", Answer: "x"})
	if got := s.Load("7"); len(got) != 1 || got[0].Request != "new" {
		t.Errorf("%+v", got)
	}
}

func TestWithoutAShellPidThereIsNoMemory(t *testing.T) {
	now := time.Now()
	s := store(t, &now)
	for _, key := range []string{"", "abc", "0", "-3", "../x"} {
		if err := s.Add(key, nl.Turn{Request: "x", Answer: "y"}); err != nil || len(s.Load(key)) != 0 {
			t.Errorf("key %q", key)
		}
	}
	if _, err := os.Stat(s.Dir); err == nil {
		t.Error("nothing should have been written")
	}
}

func TestExpiredNotesOfOtherTerminalsAreRemoved(t *testing.T) {
	now := time.Now()
	s := store(t, &now)
	_ = s.Add("1", nl.Turn{Request: "old", Answer: "x"})
	old := now.Add(-2 * TTL)
	if err := os.Chtimes(filepath.Join(s.Dir, "1.json"), old, old); err != nil {
		t.Fatal(err)
	}
	_ = s.Add("2", nl.Turn{Request: "new", Answer: "x"})
	if _, err := os.Stat(filepath.Join(s.Dir, "1.json")); err == nil {
		t.Error("the stale file should be gone")
	}
	if len(s.Load("2")) != 1 {
		t.Error("the fresh one stays")
	}
}

func TestDamagedNotesAreIgnoredAndFailuresReported(t *testing.T) {
	now := time.Now()
	s := store(t, &now)
	_ = s.Add("5", nl.Turn{Request: "a", Answer: "x"})
	if err := os.WriteFile(filepath.Join(s.Dir, "5.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(s.Load("5")) != 0 {
		t.Error("damaged notes are ignored")
	}
	if err := s.Add("5", nl.Turn{Request: "b", Answer: "x"}); err != nil || len(s.Load("5")) != 1 {
		t.Error("and replaced")
	}
	blocked := Store{Dir: filepath.Join(s.Dir, "5.json", "sub"), Now: s.Now} // a file where the folder should be
	if err := blocked.Add("5", nl.Turn{Request: "c"}); err == nil {
		t.Error("folder error")
	}
	if err := os.Mkdir(filepath.Join(s.Dir, "6.json.tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("6", nl.Turn{Request: "c"}); err == nil {
		t.Error("write error")
	}
	if err := os.Mkdir(filepath.Join(s.Dir, "8.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.Add("8", nl.Turn{Request: "c"}); err == nil {
		t.Error("rename error")
	}
}

func TestDefaultDirIsUnderTheStateFolder(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/state")
	if got := DefaultDir(); got != "/state/safeshell/conversations" {
		t.Error(got)
	}
}
