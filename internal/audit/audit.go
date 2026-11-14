// Package audit keeps an append-only record of what SafeShell decided.
package audit

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

// Entry is one decision. Outcome is "allowed", "cancelled", "ran", or
// "replaced" (a safer option was run instead of the original).
type Entry struct {
	Time     time.Time `json:"time"`
	Line     string    `json:"line"`
	Run      string    `json:"run,omitempty"`
	Level    string    `json:"level"`
	Decision string    `json:"decision"`
	Outcome  string    `json:"outcome"`
}

// Outcome names what happened to a command: the Outcome of its Entry, and the
// replacement command that ran instead (empty unless the outcome is "replaced").
// d is the decision, line the command as typed, cmd the command that was run
// (when ran is true).
func Outcome(d domain.Decision, ran bool, line, cmd string) (outcome, run string) {
	switch {
	case ran && d == domain.Allow:
		return "allowed", ""
	case ran && cmd != line:
		return "replaced", cmd
	case ran:
		return "ran", ""
	}
	return "cancelled", ""
}

// DefaultPath is $XDG_STATE_HOME/safeshell/audit.jsonl (or ~/.local/state/...).
func DefaultPath() string {
	return filepath.Join(xdg.Dir("XDG_STATE_HOME", ".local", "state"), "audit.jsonl")
}

// Append writes one line. Failure to log must never stop a command, so errors
// are returned for the caller to ignore or report.
func Append(path string, e Entry) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(e)
}

// Read loads all entries from path in chronological order. Damaged or
// unparseable lines are skipped and counted in skipped. If the file does
// not exist, it returns nil, 0, nil.
func Read(path string) ([]Entry, int, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	defer f.Close()

	var entries []Entry
	var skipped int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		if text == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(text), &e); err != nil {
			skipped++
			continue
		}
		entries = append(entries, e)
	}
	if err := scanner.Err(); err != nil {
		return entries, skipped, err
	}
	return entries, skipped, nil
}
