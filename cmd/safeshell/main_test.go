package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runSS(args ...string) (code int, out, errOut string) {
	var o, e bytes.Buffer
	code = run(args, &o, &e, false)
	return code, o.String(), e.String()
}

func TestExitCodes(t *testing.T) {
	for _, tc := range []struct {
		cmd  string
		want int
	}{
		{"ls -la", 0},
		{"cp a b", 0},
		{"rm file", 0}, // R2 asks, it does not block
		{"rm -rf build", 3},
		{"rm -rf /", 4},
	} {
		if code, _, _ := runSS("check", tc.cmd); code != tc.want {
			t.Errorf("check %q exit = %d, want %d", tc.cmd, code, tc.want)
		}
	}
}

func TestMultipleArgumentsAreJoined(t *testing.T) {
	if code, _, _ := runSS("check", "rm", "-rf", "build"); code != 3 {
		t.Errorf("unquoted words should be joined and rated; exit = %d", code)
	}
}

func TestReportContents(t *testing.T) {
	_, out, _ := runSS("check", "rm -rf ~/projects")
	for _, want := range []string{"R3 Irreversible", "BLOCK", "Safer options", "trash-put ~/projects"} {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\033[") {
		t.Error("colour codes written when colour is off")
	}
}

func TestColourWhenEnabled(t *testing.T) {
	var o, e bytes.Buffer
	run([]string{"check", "rm -rf x"}, &o, &e, true)
	if !strings.Contains(o.String(), "\033[") {
		t.Error("expected colour codes")
	}
}

func TestJSONOutput(t *testing.T) {
	code, out, _ := runSS("check", "--json", "rm -rf /")
	if code != 4 {
		t.Errorf("exit = %d, want 4", code)
	}
	var v struct {
		Level    string
		Findings []struct{ Level, Reason string }
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out)
	}
	if v.Level != "R4" || len(v.Findings) == 0 {
		t.Errorf("unexpected JSON: %+v", v)
	}
}

func TestUsageAndErrors(t *testing.T) {
	if code, out, _ := runSS(); code != 0 || !strings.Contains(out, "Usage:") {
		t.Errorf("no args should print usage and exit 0, got %d %q", code, out)
	}
	if code, _, e := runSS("check"); code != 2 || !strings.Contains(e, "Usage:") {
		t.Errorf("check with no command should exit 2 with usage, got %d", code)
	}
	if code, _, e := runSS("bogus"); code != 2 || !strings.Contains(e, `unknown command "bogus"`) {
		t.Errorf("unknown subcommand: %d %q", code, e)
	}
}
