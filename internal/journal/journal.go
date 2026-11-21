// Package journal sends audit entries to the systemd journal, so an administrator
// can read them with  journalctl -t safeshell . It speaks the journal's native
// socket protocol using only the standard library.
package journal

import (
	"fmt"
	"net"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/samarthsinh2660/safeshell/internal/audit"
)

// Socket is where the journal listens for entries.
const Socket = "/run/systemd/journal/socket"

// maxValue caps each field value in bytes. One entry has at most about ten
// fields, so it always fits in a single datagram. A longer value is cut
// (at a character boundary) and ends in "..."; the audit file keeps the whole
// line, so nothing is lost, only the journal copy is shortened.
const maxValue = 4096

// Priorities from syslog(3).
const (
	prioErr     = "3"
	prioWarning = "4"
	prioInfo    = "6"
)

type field struct{ key, value string }

// Send writes e to the journal socket at socket. It returns an error when there
// is no journal there; the caller keeps the audit file as the record.
func Send(socket string, e audit.Entry) error {
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: socket, Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("no journal at %s: %w", socket, err)
	}
	defer c.Close()
	_ = c.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)) // a full journal must not stall a command
	_, err = c.Write(encode(fields(e)))
	return err
}

// fields turns an entry into journal fields. The line and the AI reason are
// already masked in the entry.
func fields(e audit.Entry) []field {
	prio, msg := prioInfo, e.Level+" "+e.Decision+" "+e.Outcome+": "+e.Line
	switch {
	case e.Outcome == audit.OutcomeChecksOff:
		prio, msg = prioWarning, e.Line
	case e.Outcome == audit.OutcomeNotChecked:
		prio, msg = prioErr, e.Line
	case e.Decision == "block" || e.Decision == "hard-block":
		prio = prioWarning
	}
	fs := []field{
		{"MESSAGE", msg},
		{"PRIORITY", prio},
		{"SYSLOG_IDENTIFIER", "safeshell"},
	}
	for _, f := range []field{
		{"SAFESHELL_LEVEL", e.Level},
		{"SAFESHELL_DECISION", e.Decision},
		{"SAFESHELL_OUTCOME", e.Outcome},
		{"SAFESHELL_LINE", e.Line},
		{"SAFESHELL_AI_PROVIDER", e.AIProvider},
		{"SAFESHELL_AI_LEVEL", e.AILevel},
		{"SAFESHELL_AI_REASON", e.AIReason},
	} {
		if f.value != "" {
			fs = append(fs, f)
		}
	}
	if e.AIRaised {
		fs = append(fs, field{"SAFESHELL_AI_RAISED", "true"})
	}
	return fs
}

// encode is the native protocol. A value without a newline is KEY=VALUE\n. A
// value with one is KEY\n, the length as 8 bytes little-endian, the value and \n.
func encode(fs []field) []byte {
	var b []byte
	for _, f := range fs {
		v := clip(f.value)
		if !strings.Contains(v, "\n") {
			b = append(b, f.key+"="+v+"\n"...)
			continue
		}
		n := uint64(len(v))
		b = append(b, f.key+"\n"...)
		for i := 0; i < 8; i++ {
			b = append(b, byte(n>>(8*i)))
		}
		b = append(b, v+"\n"...)
	}
	return b
}

// clip cuts v to maxValue bytes without splitting a character.
func clip(v string) string {
	if len(v) <= maxValue {
		return v
	}
	cut := maxValue - 3
	for cut > 0 && !utf8.RuneStart(v[cut]) {
		cut--
	}
	return v[:cut] + "..."
}
