// Package convo remembers the last few exchanges with the assistant in one
// terminal, so a follow-up such as "now delete them" can be understood. The
// notes are private to the user, masked like the audit log, and expire.
package convo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/xdg"
)

const (
	// TTL is how long an exchange is remembered.
	TTL = 30 * time.Minute
	// MaxTurns is how many exchanges are kept per terminal.
	MaxTurns = 5
)

// DefaultDir is $XDG_STATE_HOME/safeshell/conversations.
func DefaultDir() string {
	return filepath.Join(xdg.Dir("XDG_STATE_HOME", ".local", "state"), "conversations")
}

// Store keeps one file per terminal in Dir.
type Store struct {
	Dir string
	Now func() time.Time
}

type saved struct {
	Time time.Time `json:"time"`
	nl.Turn
}

// file is the path for a terminal key, or "" when the key is not a shell pid
// (no hook, so no memory).
func (s Store) file(key string) string {
	if n, err := strconv.Atoi(key); err != nil || n <= 0 {
		return ""
	}
	return filepath.Join(s.Dir, key+".json")
}

// read returns the unexpired exchanges in path.
func read(path string, now time.Time) []saved {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var all, live []saved
	if json.Unmarshal(data, &all) != nil {
		return nil
	}
	for _, t := range all {
		if now.Sub(t.Time) <= TTL {
			live = append(live, t)
		}
	}
	return live
}

// Load returns this terminal's unexpired exchanges, oldest first.
func (s Store) Load(key string) []nl.Turn {
	var out []nl.Turn
	if p := s.file(key); p != "" {
		for _, t := range read(p, s.Now()) {
			out = append(out, t.Turn)
		}
	}
	return out
}

// Add records an exchange (secrets masked), keeps the last MaxTurns, and
// removes other terminals' notes that have expired.
func (s Store) Add(key string, t nl.Turn) error {
	p := s.file(key)
	if p == "" {
		return nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(s.Dir, 0o700)
	now := s.Now()
	t.Request, t.Command, t.Answer = report.MaskSecrets(t.Request), report.MaskSecrets(t.Command), report.MaskSecrets(t.Answer)
	masked := make([]string, len(t.Clarifications))
	for i, c := range t.Clarifications {
		masked[i] = report.MaskSecrets(c)
	}
	t.Clarifications = masked
	all := append(read(p, now), saved{Time: now, Turn: t})
	data, _ := json.Marshal(all[max(0, len(all)-MaxTurns):]) // plain strings and numbers cannot fail to encode
	s.prune(now)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// prune deletes note files nobody has touched within the TTL.
func (s Store) prune(now time.Time) {
	des, _ := os.ReadDir(s.Dir)
	for _, de := range des {
		if fi, err := de.Info(); err == nil && now.Sub(fi.ModTime()) > TTL {
			_ = os.Remove(filepath.Join(s.Dir, de.Name()))
		}
	}
}
