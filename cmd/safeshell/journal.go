package main

import (
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/audit"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/journal"
)

// journalSocket is where the systemd journal listens; tests replace it.
var journalSocket = journal.Socket

// teamJournal is true when the administrator's policy asks for the journal; tests replace it.
var teamJournal = func() bool { return config.LoadPolicyJournal(config.PolicyPath, 0) }

// logEntry writes e to the audit file and, when the config or the team policy
// asks for it, to the systemd journal as well. A full disk or a machine with no
// journal must never stop a command, so neither failure is reported.
func logEntry(e audit.Entry) {
	_ = audit.Append(auditPath(), e)
	if c, err := config.Load(config.Path()); (err == nil && c.Audit.Journal) || teamJournal() {
		_ = journal.Send(journalSocket, e)
	}
}

// runNote is what the shell hooks call, in the background, to log an event that
// is not a command: checks turned off with SAFESHELL_OFF, or SafeShell failing
// so a line went through unchecked. It does nothing else, so it is fast.
func runNote(args []string, stderr io.Writer) int {
	var line, outcome string
	switch {
	case len(args) == 1 && args[0] == audit.OutcomeChecksOff:
		outcome, line = audit.OutcomeChecksOff, "SAFESHELL_OFF is set in a terminal, so its commands are not checked"
	case len(args) == 2 && args[0] == audit.OutcomeNotChecked:
		if _, err := strconv.Atoi(args[1]); err != nil {
			fmt.Fprintln(stderr, "safeshell note: the exit status must be a number")
			return 2
		}
		outcome, line = audit.OutcomeNotChecked, "SafeShell failed (exit "+args[1]+"), so a line was run without being checked"
	default:
		fmt.Fprintln(stderr, "safeshell note: use  checks-off  or  not-checked EXIT_STATUS  (the shell hook calls this)")
		return 2
	}
	logEntry(audit.Entry{Time: time.Now().UTC(), Line: line, Outcome: outcome})
	return 0
}
