package audit

import "testing"

func TestIsEventIsTrueOnlyForTheTwoEvents(t *testing.T) {
	for outcome, want := range map[string]bool{OutcomeChecksOff: true, OutcomeNotChecked: true, "ran": false, "cancelled": false, "": false} {
		if got := (Entry{Outcome: outcome}).IsEvent(); got != want {
			t.Errorf("%q: %v", outcome, got)
		}
	}
}
