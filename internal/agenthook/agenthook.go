// Package agenthook speaks the JSON that AI coding agents use to ask whether a
// command may run, and holds the ready-made snippets that connect an agent to
// `safeshell guard --agent`. It reads and writes bytes only; the decision is
// made by the caller.
package agenthook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxInput is the most input read: a command line is never this long.
const maxInput = 1 << 20

// Request is the command an agent wants to run. Claude is true when it came in
// the Claude Code hook format, which must be answered in that format.
type Request struct {
	Command, Cwd string
	Claude       bool
}

// Answer is what is said back.
type Answer struct {
	Allow  bool
	Level  string // "R0" to "R4"; empty when the input could not be read
	Reason string
	Safer  string // a safer command, when there is one
}

// input holds the fields of both formats; which are set says which format it is.
type input struct {
	Command   string `json:"command"`
	Cwd       string `json:"cwd"`
	Event     string `json:"hook_event_name"`
	Tool      string `json:"tool_name"`
	ToolInput *struct {
		Command string `json:"command"`
	} `json:"tool_input"`
}

// Parse reads one JSON object: either a Claude Code PreToolUse hook input
// (tool_name "Bash", tool_input.command, cwd) or {"command": "...", "cwd": "..."}.
// The error is plain English and is safe to show as the reason for a denial.
func Parse(r io.Reader) (Request, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxInput+1))
	if err != nil || len(b) > maxInput {
		return Request{}, errors.New("the input could not be read; send one JSON object on stdin")
	}
	var in input
	if err := json.Unmarshal(b, &in); err != nil {
		return Request{}, errors.New("the input is not one JSON object; send {\"command\": \"...\", \"cwd\": \"...\"}")
	}
	if in.ToolInput != nil || in.Tool != "" || in.Event != "" {
		return parseClaude(in)
	}
	if in.Command == "" {
		return Request{}, errors.New("the input has no command; send {\"command\": \"...\", \"cwd\": \"...\"}")
	}
	return Request{Command: in.Command, Cwd: in.Cwd}, nil
}

func parseClaude(in input) (Request, error) {
	switch {
	case in.Event != "" && in.Event != "PreToolUse":
		return Request{}, fmt.Errorf("SafeShell only checks PreToolUse hooks, not %q", in.Event)
	case in.Tool != "Bash":
		return Request{}, errors.New("SafeShell only checks Bash commands; set the hook's matcher to Bash")
	case in.ToolInput == nil || in.ToolInput.Command == "":
		return Request{}, errors.New("the Bash call has no command to check")
	}
	return Request{Command: in.ToolInput.Command, Cwd: in.Cwd, Claude: true}, nil
}

// Format is the JSON to print for a. A Claude Code request gets the PreToolUse
// decision (to be sent with exit status 0, which is how Claude Code reads it);
// anything else gets {"decision", "level", "reason", "safer"}.
func Format(claude bool, a Answer) []byte {
	var v any
	decision := "deny"
	if a.Allow {
		decision = "allow"
	}
	if claude && a.Allow {
		// No decision: Claude Code's own permission rules still apply. An
		// "allow" here would skip them, and SafeShell only ever adds checks.
		return nil
	}
	if claude {
		reason := a.Reason
		if a.Safer != "" {
			reason += " Safer option: " + a.Safer
		}
		v = map[string]any{"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       decision,
			"permissionDecisionReason": reason,
		}}
	} else {
		v = struct {
			Decision string `json:"decision"`
			Level    string `json:"level,omitempty"`
			Reason   string `json:"reason"`
			Safer    string `json:"safer,omitempty"`
		}{decision, a.Level, a.Reason, a.Safer}
	}
	b, _ := json.Marshal(v) // strings and maps of strings always encode
	return append(b, '\n')
}

// Claude is the entry to merge into the "hooks" part of Claude Code's
// settings.json (~/.claude/settings.json, or .claude/settings.json in a project).
const Claude = `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          { "type": "command", "command": "safeshell guard --agent", "timeout": 60 }
        ]
      }
    ]
  }
}
`

// Wrapper is a script that runs the one command it is given only when SafeShell
// allows it. It is for agents and scripts that run commands through a tool you
// choose, and needs jq.
const Wrapper = `#!/bin/sh
# safeshell-run 'command': run the command only if SafeShell allows it (needs jq).
[ $# -eq 1 ] || { echo "usage: safeshell-run 'command'" >&2; exit 2; }
answer=$(jq -n --arg command "$1" --arg cwd "$PWD" '{command: $command, cwd: $cwd}' | safeshell guard --agent) || {
  printf '%s\n' "$answer" | jq -r '"SafeShell did not run it: " + .reason + (if .safer then "\nSafer option: " + .safer else "" end)' >&2
  exit 1
}
exec sh -c "$1"
`

// Snippet returns the named snippet: "claude" or "wrapper".
func Snippet(name string) (string, error) {
	switch name {
	case "claude":
		return Claude, nil
	case "wrapper":
		return Wrapper, nil
	}
	return "", fmt.Errorf("unknown agent %q; choose claude or wrapper", name)
}
