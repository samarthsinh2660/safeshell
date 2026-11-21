// Package host is the part of undo that touches the machine: it reads apt's
// history log and asks systemctl about a unit. It only reads; nothing here
// changes the system.
package host

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/undo"
)

// AptHistoryPath is where apt logs each transaction.
const AptHistoryPath = "/var/log/apt/history.log"

// AptHistory reads the transactions logged at path. A missing log is an error
// the caller can show: the plan is then simply not offered.
func AptHistory(path string) ([]undo.AptTx, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return undo.ParseAptHistory(string(b), time.Local), nil
}

// Runner runs a program and returns what it printed, whatever its exit status
// (systemctl is-active exits non-zero for a unit that is not running).
type Runner func(ctx context.Context, name string, args ...string) string

// Exec is the Runner that starts the real program.
func Exec(ctx context.Context, name string, args ...string) string {
	out, _ := exec.CommandContext(ctx, name, args...).Output()
	return strings.TrimSpace(string(out))
}

// UnitState reads whether unit is running and whether it starts at boot. ok is
// false when systemctl did not answer either question, so no plan is built on a guess.
func UnitState(ctx context.Context, run Runner, unit string) (st undo.UnitState, ok bool) {
	st.Active = run(ctx, "systemctl", "is-active", unit)
	st.Enabled = run(ctx, "systemctl", "is-enabled", unit)
	return st, st.Active != "" && st.Enabled != ""
}
