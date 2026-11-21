package journal

import (
	"encoding/binary"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
)

// listen is a fake journal: a datagram socket in a temp folder.
func listen(t *testing.T) (*net.UnixConn, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.sock")
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: path, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c, path
}

// parse reads a datagram the way journald does.
func parse(t *testing.T, b []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	for len(b) > 0 {
		nl := strings.IndexByte(string(b), '\n')
		if nl < 0 {
			t.Fatalf("field without a newline: %q", b)
		}
		head := string(b[:nl])
		if k, v, ok := strings.Cut(head, "="); ok {
			out[k] = v
			b = b[nl+1:]
			continue
		}
		b = b[nl+1:]
		n := binary.LittleEndian.Uint64(b[:8])
		out[head] = string(b[8 : 8+n])
		if b[8+n] != '\n' {
			t.Fatalf("binary value of %s does not end in a newline", head)
		}
		b = b[8+n+1:]
	}
	return out
}

func receive(t *testing.T, c *net.UnixConn) map[string]string {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 1<<16)
	n, err := c.Read(b)
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, b[:n])
}

func TestSendWritesEveryField(t *testing.T) {
	c, path := listen(t)
	e := audit.Entry{Line: "rm -rf build", Level: "R3", Decision: "block", Outcome: "cancelled",
		AIProvider: "local", AIRaised: true, AILevel: "R3", AIReason: "deletes the build folder"}
	if err := Send(path, e); err != nil {
		t.Fatal(err)
	}
	got := receive(t, c)
	want := map[string]string{
		"MESSAGE": "R3 block cancelled: rm -rf build", "PRIORITY": "4", "SYSLOG_IDENTIFIER": "safeshell",
		"SAFESHELL_LEVEL": "R3", "SAFESHELL_DECISION": "block", "SAFESHELL_OUTCOME": "cancelled", "SAFESHELL_LINE": "rm -rf build",
		"SAFESHELL_AI_PROVIDER": "local", "SAFESHELL_AI_LEVEL": "R3", "SAFESHELL_AI_RAISED": "true", "SAFESHELL_AI_REASON": "deletes the build folder",
	}
	if len(got) != len(want) {
		t.Errorf("got %v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestEntryWithoutAIHasNoAIFields(t *testing.T) {
	c, path := listen(t)
	if err := Send(path, audit.Entry{Line: "ls", Level: "R0", Decision: "allow", Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	got := receive(t, c)
	if got["PRIORITY"] != "6" {
		t.Errorf("priority %q", got["PRIORITY"])
	}
	for k := range got {
		if strings.HasPrefix(k, "SAFESHELL_AI_") {
			t.Errorf("unexpected %s", k)
		}
	}
}

func TestPriorityAndMessageOfEvents(t *testing.T) {
	for _, tc := range []struct {
		e    audit.Entry
		prio string
	}{
		{audit.Entry{Line: "checks are off", Outcome: audit.OutcomeChecksOff}, "4"},
		{audit.Entry{Line: "SafeShell failed", Outcome: audit.OutcomeNotChecked}, "3"},
		{audit.Entry{Line: "x", Level: "R4", Decision: "hard-block", Outcome: "cancelled"}, "4"},
	} {
		c, path := listen(t)
		if err := Send(path, tc.e); err != nil {
			t.Fatal(err)
		}
		got := receive(t, c)
		if got["PRIORITY"] != tc.prio {
			t.Errorf("%s: priority %q, want %s", tc.e.Outcome, got["PRIORITY"], tc.prio)
		}
		if tc.e.IsEvent() && got["MESSAGE"] != tc.e.Line {
			t.Errorf("%s: message %q", tc.e.Outcome, got["MESSAGE"])
		}
	}
}

func TestValuesWithNewlinesUseTheLengthPrefixedForm(t *testing.T) {
	c, path := listen(t)
	line := "cat <<EOF\nhello\nEOF"
	if err := Send(path, audit.Entry{Line: line, Level: "R0", Decision: "allow", Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	got := receive(t, c)
	if got["SAFESHELL_LINE"] != line {
		t.Errorf("line %q", got["SAFESHELL_LINE"])
	}
	if got["SAFESHELL_LEVEL"] != "R0" {
		t.Errorf("a field after a binary one was lost: %v", got)
	}
}

func TestEncodeMatchesTheProtocolExactly(t *testing.T) {
	got := encode([]field{{"A", "x"}, {"B", "l1\nl2"}})
	want := "A=x\nB\n\x05\x00\x00\x00\x00\x00\x00\x00l1\nl2\n"
	if string(got) != want {
		t.Errorf("%q, want %q", got, want)
	}
}

func TestLongValuesAreCutOnACharacterBoundary(t *testing.T) {
	c, path := listen(t)
	long := strings.Repeat("é", maxValue) // two bytes each, so the cut would fall inside a character at an odd offset
	if err := Send(path, audit.Entry{Line: long, Level: "R0", Decision: "allow", Outcome: "allowed"}); err != nil {
		t.Fatal(err)
	}
	got := receive(t, c)["SAFESHELL_LINE"]
	if len(got) > maxValue || !strings.HasSuffix(got, "...") || !strings.HasPrefix(long, strings.TrimSuffix(got, "...")) {
		t.Errorf("%d bytes, %q...", len(got), got[:10])
	}
	if short := clip("short"); short != "short" {
		t.Errorf("short value changed: %q", short)
	}
	if got := clip(strings.Repeat("a", maxValue+1)); len(got) != maxValue {
		t.Errorf("%d bytes", len(got))
	}
}

func TestNoJournalIsAnErrorTheCallerCanIgnore(t *testing.T) {
	err := Send(filepath.Join(t.TempDir(), "missing.sock"), audit.Entry{Line: "ls"})
	if err == nil || !strings.Contains(err.Error(), "no journal") {
		t.Errorf("%v", err)
	}
}
