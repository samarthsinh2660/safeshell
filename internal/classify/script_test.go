package classify

import (
	"slices"
	"strings"
	"testing"
)

func TestScriptRatesTheLineOfEachCommand(t *testing.T) {
	src := "#!/bin/bash\nDIR=/tmp/x\nls\nrm -rf \"$DIR\"/\n"
	got, err := Script(src, "/home/u")
	if err != nil {
		t.Fatal(err)
	}
	var rm *LineFinding
	for i := range got {
		if got[i].Line == 4 {
			rm = &got[i]
		}
	}
	if rm == nil || rm.Level != R3 || rm.Text != `rm -rf "$DIR"/` || len(rm.Reasons) == 0 {
		t.Fatalf("line 4 should be R3 with its text and reasons: %+v", got)
	}
	if ScriptLevel(got) != R3 {
		t.Errorf("overall level = %v, want R3", ScriptLevel(got))
	}
	if !slices.IsSortedFunc(got, func(a, b LineFinding) int { return a.Line - b.Line }) {
		t.Errorf("not sorted by line: %+v", got)
	}
}

func TestScriptConstructs(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		line      int
		level     Level
		absent    []int // lines that must not be listed
	}{
		{"function body", "f() {\n  echo hi\n  rm -rf /tmp/x\n}\nf\n", 3, R3, nil},
		{"if body", "if true; then\n  ls\n  rm -rf /tmp/x\nfi\n", 3, R3, nil},
		{"for loop", "for d in a b; do\n  rm -rf /tmp/$d\ndone\n", 2, R3, nil},
		{"while loop", "while true; do\n  rm -rf /tmp/x\n  break\ndone\n", 2, R3, nil},
		{"pipeline", "ls |\n  xargs rm -rf\n", 1, R3, nil},
		{"subshell", "(\n  rm -rf /tmp/x\n)\n", 2, R3, nil},
		{"and list on later line", "true &&\n  rm -rf /tmp/x\n", 2, R3, nil},
		{"multi-line command reports the start", "rm -rf /tmp/x \\\n  /tmp/y\n", 1, R3, []int{2}},
		{"heredoc body is not code", "cat <<EOF\nrm -rf /\nEOF\nls\n", 1, R0, []int{2, 3}},
		{"heredoc to a shell is code", "sh <<EOF\nrm -rf /tmp/x\nEOF\n", 1, R3, nil},
		{"comments and blanks ignored", "# rm -rf /\n\n   # ls\nls\n", 4, R0, []int{1, 2, 3}},
		{"sh -c takes the line of its statement", "echo a\nsh -c 'rm -rf /tmp/x'\n", 2, R3, nil},
		{"fork bomb", "echo a\n:(){ :|:& };:\n", 2, R4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Script(tc.src, "/home/u")
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, lf := range got {
				if lf.Line == tc.line {
					found = true
					if lf.Level != tc.level {
						t.Errorf("line %d = %v, want %v", lf.Line, lf.Level, tc.level)
					}
				}
				if slices.Contains(tc.absent, lf.Line) {
					t.Errorf("line %d should not be listed", lf.Line)
				}
			}
			if !found {
				t.Errorf("line %d missing: %+v", tc.line, got)
			}
		})
	}
}

func TestScriptFollowsCdBetweenLines(t *testing.T) {
	moved, _ := Script("cd /\nrm -rf *\n", "/home/u")
	plain, _ := Script("rm -rf *\n", "/home/u")
	if len(moved) != 2 || len(plain) != 1 {
		t.Fatalf("got %+v / %+v", moved, plain)
	}
	if moved[1].Level <= plain[0].Level {
		t.Errorf("after cd /, * is /*: %v, without the cd %v", moved[1].Level, plain[0].Level)
	}
}

func TestScriptParseErrorNamesTheLine(t *testing.T) {
	_, err := Script("echo ok\nif true; then\n  ls\n", "")
	if err == nil || !strings.HasPrefix(err.Error(), "line ") {
		t.Fatalf("err = %v", err)
	}
	_, err = Script("echo ok\necho 'unterminated\n", "")
	if err == nil || !strings.HasPrefix(err.Error(), "line 2") {
		t.Errorf("err = %v, want line 2", err)
	}
}

func TestScriptLevelOfNothingIsR0(t *testing.T) {
	if ScriptLevel(nil) != R0 {
		t.Error("an empty script is R0")
	}
}

func TestClassifyForkBomb(t *testing.T) {
	if v := Classify(":(){ :|:& };:", ""); v.Level != R4 {
		t.Errorf("level = %v, want R4", v.Level)
	}
}
