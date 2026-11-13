package sandbox

import "time"

// Spec describes one simulated run.
type Spec struct {
	Line      string        // shell text to run, exactly as the user typed it
	Cwd       string        // working directory; "" means the caller's
	Env       []string      // environment; nil means the caller's
	Shell     string        // "" means /bin/bash, falling back to /bin/sh
	Timeout   time.Duration // wall-clock limit; 0 means 30s
	MaxOutput int           // bytes kept of each of stdout and stderr; 0 means 64 KiB
}

// Result is what the simulation observed.
type Result struct {
	ExitCode  int
	TimedOut  bool
	Stdout    string
	Stderr    string
	Diff      FileDiff
	Net       []NetAttempt // network operations the command tried; the sandbox has no route out
	Duration  time.Duration
	Truncated bool // output was cut at MaxOutput
	// Warnings list parts of the filesystem that could not be mirrored; they
	// are empty inside the sandbox, so commands touching them behave differently.
	Warnings []Warning
}

// Warning is one part of the filesystem the sandbox could not mirror.
type Warning struct {
	Paths []string `json:"paths"` // the directories it concerns
	Text  string   `json:"text"`
	// Writable lists the paths the real user can write to. A change there that
	// the sandbox could not see would go unrecorded; elsewhere it cannot happen.
	Writable []string `json:"writable,omitempty"`
}

// Defaults fills in the zero values of s.
func (s Spec) Defaults() Spec {
	if s.Timeout == 0 {
		s.Timeout = 30 * time.Second
	}
	if s.MaxOutput == 0 {
		s.MaxOutput = 64 << 10
	}
	return s
}
