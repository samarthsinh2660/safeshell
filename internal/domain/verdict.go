package domain

import (
	"encoding/json"
	"fmt"
)

// Level is the reversibility class of a command. Higher is more dangerous.
type Level int

const (
	R0 Level = iota // read-only
	R1              // reversible
	R2              // recoverable with a snapshot, or small
	R3              // irreversible
	R4              // catastrophic
)

var labels = [...]string{"Read-only", "Reversible", "Recoverable", "Irreversible", "Catastrophic"}

func (l Level) String() string { return [...]string{"R0", "R1", "R2", "R3", "R4"}[l] }

// Label is the human-readable class name.
func (l Level) Label() string { return labels[l] }

// MarshalJSON writes the level as "R3" rather than 3.
func (l Level) MarshalJSON() ([]byte, error) { return json.Marshal(l.String()) }

// UnmarshalJSON reads "R3" back into a Level, so analyses can cross the daemon socket.
func (l *Level) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, ok := ParseLevel(s)
	if !ok {
		return fmt.Errorf("unknown level %q", s)
	}
	*l = v
	return nil
}

// ParseLevel reads "R0" to "R4" exactly; ok is false for anything else.
func ParseLevel(s string) (Level, bool) {
	for l := R0; l <= R4; l++ {
		if l.String() == s {
			return l, true
		}
	}
	return 0, false
}

// ExitCode is the process exit status for a command rated l: 4 for R4, 3 for R3, otherwise 0.
func (l Level) ExitCode() int {
	switch l {
	case R4:
		return 4
	case R3:
		return 3
	}
	return 0
}

// Alternative is a safer command offered instead of the one typed.
type Alternative struct {
	Command string `json:"command"`
	Note    string `json:"note"`
}

// Finding is one reason a command received its level.
type Finding struct {
	Level   Level        `json:"level"`
	Command string       `json:"command"`
	Reason  string       `json:"reason"`
	Alt     *Alternative `json:"alternative,omitempty"`
}

// Verdict is the outcome of classifying a whole command line.
type Verdict struct {
	Level        Level         `json:"level"`
	Findings     []Finding     `json:"findings"`
	Alternatives []Alternative `json:"alternatives,omitempty"`
	// Unclassified lists commands the rules do not know. They are rated R1 here;
	// the sandbox run and the AI review can raise that.
	Unclassified []string `json:"unclassified,omitempty"`
}

// WithAlt returns f with the safer command to suggest for it.
func (f Finding) WithAlt(cmd, note string) Finding {
	f.Alt = &Alternative{Command: cmd, Note: note}
	return f
}

// MaxLevel is the highest level among fs, or R0 if there are none.
func MaxLevel(fs []Finding) Level {
	m := R0
	for _, f := range fs {
		m = max(m, f.Level)
	}
	return m
}
