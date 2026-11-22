package sandbox

import (
	"strings"
	"testing"
)

func TestIncomplete(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Result
		want string // part of the reason, "" for a trustworthy run
	}{
		{"clean run", Result{}, ""},
		{"failed for its own reasons", Result{ExitCode: 1, Stderr: "rm: cannot remove 'x': Permission denied"}, ""},
		{"overflow", Result{ExitCode: 1, Stderr: "rm: cannot remove 'proj/a.txt': Value too large for defined data type"}, "Value too large"},
		{"cross device", Result{ExitCode: 1, Stderr: "mv: Invalid cross-device link"}, "cross-device"},
		{"no space", Result{ExitCode: 1, Stderr: "cp: write error: No space left on device"}, "No space left"},
		{"sandbox error but exit 0", Result{Stderr: "No space left on device"}, ""},
		{"scratch full", Result{ScratchFull: true}, "ran out of space"},
		{"timed out is reported elsewhere", Result{TimedOut: true, ExitCode: -1, ScratchFull: true}, ""},
	} {
		got := Incomplete(tc.r)
		if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to contain %q", tc.name, got, tc.want)
		}
	}
}
