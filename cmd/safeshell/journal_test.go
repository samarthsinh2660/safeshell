package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
)

// fakeJournal listens on a socket in a temp folder and points journalSocket at it.
func fakeJournal(t *testing.T) *net.UnixConn {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.sock")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	old := journalSocket
	journalSocket = path
	t.Cleanup(func() { journalSocket = old; c.Close() })
	return c
}

// sent returns what the fake journal received, or "" when nothing came.
func sent(c *net.UnixConn) string {
	_ = c.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	b := make([]byte, 1<<16)
	n, err := c.Read(b)
	if err != nil {
		return ""
	}
	return string(b[:n])
}

// journalConfig writes a config file asking (or not) for the journal.
func journalConfig(t *testing.T, on bool) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	c := config.Config{}
	c.Audit.Journal = on
	if err := config.Save(config.Path(), c); err != nil {
		t.Fatal(err)
	}
}

func TestEntriesGoToTheJournalWhenTheConfigAsksAndAlwaysToTheFile(t *testing.T) {
	c := fakeJournal(t)
	journalConfig(t, true)
	logEntry(audit.Entry{Line: "rm -rf x", Level: "R3", Decision: "block", Outcome: "cancelled"})
	if got := sent(c); !strings.Contains(got, "SAFESHELL_LINE=rm -rf x\n") || !strings.Contains(got, "SYSLOG_IDENTIFIER=safeshell\n") {
		t.Errorf("journal got %q", got)
	}
	if e := lastAudit(t); e.Line != "rm -rf x" {
		t.Errorf("the file must be written too: %+v", e)
	}
}

func TestEntriesStayOutOfTheJournalByDefault(t *testing.T) {
	c := fakeJournal(t)
	journalConfig(t, false)
	logEntry(audit.Entry{Line: "ls-by-default", Level: "R0", Decision: "allow", Outcome: "allowed"})
	if got := sent(c); got != "" {
		t.Errorf("journal got %q", got)
	}
	if e := lastAudit(t); e.Line != "ls-by-default" {
		t.Errorf("%+v", e)
	}
}

func TestTheTeamPolicyTurnsTheJournalOnForEveryone(t *testing.T) {
	c := fakeJournal(t)
	journalConfig(t, false)
	old := teamJournal
	t.Cleanup(func() { teamJournal = old })
	teamJournal = func() bool { return true }
	logEntry(audit.Entry{Line: "team-line", Level: "R0", Decision: "allow", Outcome: "allowed"})
	if got := sent(c); !strings.Contains(got, "SAFESHELL_LINE=team-line\n") {
		t.Errorf("journal got %q", got)
	}
}

func TestNoJournalOnTheMachineChangesNothing(t *testing.T) {
	journalConfig(t, true) // journalSocket points at a socket that does not exist
	logEntry(audit.Entry{Line: "no-journal-here", Level: "R0", Decision: "allow", Outcome: "allowed"})
	if e := lastAudit(t); e.Line != "no-journal-here" {
		t.Errorf("%+v", e)
	}
}

func TestGuardDecisionsAreMaskedBeforeTheyReachTheJournal(t *testing.T) {
	c := fakeJournal(t)
	journalConfig(t, true)
	record(engine.Analysis{Line: "curl -u bob:hunter2 http://x", Decision: domain.Allow}, "curl -u bob:hunter2 http://x", true)
	got := sent(c)
	if got == "" || strings.Contains(got, "hunter2") {
		t.Errorf("journal got %q", got)
	}
}

func TestNoteLogsTheTwoHookEvents(t *testing.T) {
	c := fakeJournal(t)
	journalConfig(t, true)
	if code, out, errOut := runSS("note", "checks-off"); code != 0 || out != "" || errOut != "" {
		t.Fatalf("%d %q %q", code, out, errOut)
	}
	if e := lastAudit(t); e.Outcome != audit.OutcomeChecksOff || e.Level != "" || !e.IsEvent() {
		t.Errorf("%+v", e)
	}
	if got := sent(c); !strings.Contains(got, "SAFESHELL_OUTCOME=checks-off\n") || !strings.Contains(got, "PRIORITY=4\n") {
		t.Errorf("journal got %q", got)
	}
	if code, _, _ := runSS("note", "not-checked", "127"); code != 0 {
		t.Fatal(code)
	}
	if e := lastAudit(t); e.Outcome != audit.OutcomeNotChecked || !strings.Contains(e.Line, "exit 127") {
		t.Errorf("%+v", e)
	}
	if got := sent(c); !strings.Contains(got, "PRIORITY=3\n") {
		t.Errorf("journal got %q", got)
	}
}

func TestNoteRefusesWhatItDoesNotKnow(t *testing.T) {
	for _, args := range [][]string{{"note"}, {"note", "other"}, {"note", "checks-off", "1"}, {"note", "not-checked"}, {"note", "not-checked", "x"}} {
		before, _ := os.ReadFile(auditPath())
		if code, _, errOut := runSS(args...); code != 2 || !strings.Contains(errOut, "safeshell note:") {
			t.Errorf("%v: %d %q", args, code, errOut)
		}
		if after, _ := os.ReadFile(auditPath()); string(after) != string(before) {
			t.Errorf("%v wrote to the log", args)
		}
	}
}

func TestEventsAreNotShownToTheAIAsRecentCommands(t *testing.T) {
	runSS("note", "checks-off")
	for _, r := range recentCommands() {
		if strings.Contains(r.Line, "SAFESHELL_OFF") {
			t.Errorf("an event was listed as a command: %+v", r)
		}
	}
}
