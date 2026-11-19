package agenthook

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     Request
		err      string
	}{
		{"generic", `{"command":"ls -l","cwd":"/work"}`, Request{Command: "ls -l", Cwd: "/work"}, ""},
		{"generic no cwd", `{"command":"ls"}`, Request{Command: "ls"}, ""},
		{"claude", `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls","description":"d"},"cwd":"/w"}`, Request{Command: "ls", Cwd: "/w", Claude: true}, ""},
		{"claude without event", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, Request{Command: "ls", Claude: true}, ""},
		{"other event", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`, Request{}, "PostToolUse"},
		{"other tool", `{"hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"x"}}`, Request{}, "matcher to Bash"},
		{"claude without command", `{"tool_name":"Bash","tool_input":{}}`, Request{}, "no command"},
		{"claude without input", `{"tool_name":"Bash"}`, Request{}, "no command"},
		{"empty", ``, Request{}, "not one JSON object"},
		{"malformed", `{"command":`, Request{}, "not one JSON object"},
		{"not an object", `["ls"]`, Request{}, "not one JSON object"},
		{"no command", `{"cwd":"/w"}`, Request{}, "no command"},
		{"empty command", `{"command":""}`, Request{}, "no command"},
	} {
		got, err := Parse(strings.NewReader(tc.in))
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s: err %v, want %q", tc.name, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s: got %+v, %v want %+v", tc.name, got, err, tc.want)
		}
	}
}

func TestParseTooLong(t *testing.T) {
	in := `{"command":"` + strings.Repeat("a", maxInput) + `"}`
	if _, err := Parse(strings.NewReader(in)); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err %v", err)
	}
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestParseReadError(t *testing.T) {
	if _, err := Parse(failing{}); err == nil || !strings.Contains(err.Error(), "could not be read") {
		t.Errorf("err %v", err)
	}
}

func TestFormatGeneric(t *testing.T) {
	var got map[string]string
	b := Format(false, Answer{Level: "R3", Reason: "no", Safer: "trash-put x"})
	if err := json.Unmarshal(b, &got); err != nil || got["decision"] != "deny" || got["level"] != "R3" || got["reason"] != "no" || got["safer"] != "trash-put x" {
		t.Errorf("%s %v", b, err)
	}
	got = nil
	json.Unmarshal(Format(false, Answer{Allow: true, Level: "R0", Reason: "ok"}), &got)
	if got["decision"] != "allow" {
		t.Errorf("%v", got)
	}
	if _, has := got["safer"]; has {
		t.Errorf("an empty safer option must be left out: %v", got)
	}
}

func TestFormatClaude(t *testing.T) {
	var got struct {
		H map[string]string `json:"hookSpecificOutput"`
	}
	b := Format(true, Answer{Reason: "no.", Safer: "trash-put x"})
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.H["hookEventName"] != "PreToolUse" || got.H["permissionDecision"] != "deny" || got.H["permissionDecisionReason"] != "no. Safer option: trash-put x" {
		t.Errorf("%s", b)
	}
	json.Unmarshal(Format(true, Answer{Allow: true, Reason: "ok"}), &got)
	if got.H["permissionDecision"] != "allow" || got.H["permissionDecisionReason"] != "ok" {
		t.Errorf("%v", got.H)
	}
}

func TestSnippets(t *testing.T) {
	c, err := Snippet("claude")
	var v struct {
		Hooks map[string][]struct {
			Matcher string
			Hooks   []struct{ Type, Command string }
		}
	}
	if err != nil || json.Unmarshal([]byte(c), &v) != nil {
		t.Fatalf("claude snippet is not JSON: %v", err)
	}
	if h := v.Hooks["PreToolUse"]; len(h) != 1 || h[0].Matcher != "Bash" || h[0].Hooks[0].Command != "safeshell guard --agent" {
		t.Errorf("%+v", v)
	}
	w, err := Snippet("wrapper")
	if err != nil || !strings.HasPrefix(w, "#!/bin/sh\n") || !strings.Contains(w, "safeshell guard --agent") {
		t.Errorf("wrapper: %v", err)
	}
	if _, err := Snippet("codex"); err == nil || !strings.Contains(err.Error(), "claude or wrapper") {
		t.Errorf("err %v", err)
	}
}
