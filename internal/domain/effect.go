package domain

import (
	"encoding/json"
	"fmt"
)

// Evidence says how we know an effect, strongest first.
type Evidence int

const (
	Estimated Evidence = iota // static reasoning only; never auto-runs
	Inspected                 // read from the real current state, read-only
	Previewed                 // the tool's own dry run
	Observed                  // measured by really running it in the sandbox
)

func (e Evidence) String() string {
	return [...]string{"ESTIMATED", "INSPECTED", "PREVIEWED", "OBSERVED"}[e]
}

// EffectKind names the part of the system an effect touches.
type EffectKind string

const (
	EffectFS         EffectKind = "files"
	EffectProcess    EffectKind = "processes"
	EffectDisk       EffectKind = "disk"
	EffectNetwork    EffectKind = "network"
	EffectReview     EffectKind = "review"
	EffectPackages   EffectKind = "packages"
	EffectServices   EffectKind = "services"
	EffectGit        EffectKind = "git"
	EffectContainers EffectKind = "containers"
)

// Effect is one thing a command would do, with how sure we are.
type Effect struct {
	Kind     EffectKind `json:"kind"`
	Evidence Evidence   `json:"evidence"`
	Summary  string     `json:"summary"`
	Details  []string   `json:"details,omitempty"`
	// MinLevel is the lowest risk level this effect justifies. Effects can raise
	// a command's level, never lower it.
	MinLevel Level `json:"min_level"`
}

// MarshalJSON writes the evidence as its name.
func (e Evidence) MarshalJSON() ([]byte, error) { return []byte(`"` + e.String() + `"`), nil }

// UnmarshalJSON reads an evidence name back.
func (e *Evidence) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for _, v := range []Evidence{Estimated, Inspected, Previewed, Observed} {
		if v.String() == s {
			*e = v
			return nil
		}
	}
	return fmt.Errorf("unknown evidence %q", s)
}

// Decision is what SafeShell does with a command.
type Decision int

const (
	Allow     Decision = iota // run it
	Confirm                   // show the impact and ask
	Block                     // refuse unless the user overrides
	HardBlock                 // refuse; overriding needs a typed phrase
)

func (d Decision) String() string {
	return [...]string{"allow", "confirm", "block", "hard-block"}[d]
}

// ExitCode is the process exit status for d: 4 for a hard block, 3 for a block, otherwise 0.
func (d Decision) ExitCode() int {
	switch d {
	case HardBlock:
		return 4
	case Block:
		return 3
	}
	return 0
}

// MarshalJSON writes the decision as its name.
func (d Decision) MarshalJSON() ([]byte, error) { return []byte(`"` + d.String() + `"`), nil }

// UnmarshalJSON reads a decision name back.
func (d *Decision) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	for _, v := range []Decision{Allow, Confirm, Block, HardBlock} {
		if v.String() == s {
			*d = v
			return nil
		}
	}
	return fmt.Errorf("unknown decision %q", s)
}
