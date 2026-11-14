package shellparse

import (
	"slices"
	"testing"
)

func TestSedActions(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		execs, writes []string
	}{
		{[]string{"-n", "1e rm -rf /", "f"}, []string{"rm -rf /"}, nil},
		{[]string{"s/x/y/e"}, []string{""}, nil},
		{[]string{"s/x/y/gw out.txt"}, nil, []string{"out.txt"}},
		{[]string{"-e", "w a", "-e", "W b"}, nil, []string{"a", "b"}},
		{[]string{"-ew a"}, nil, []string{"a"}},
		{[]string{"--expression=w a"}, nil, []string{"a"}},
		{[]string{"--expression", "w a"}, nil, []string{"a"}},
		{[]string{"-e"}, nil, nil},
		{[]string{"-f", "s.sed", "-l", "5", "--file", "x", "--line-length", "3"}, nil, nil},
		{[]string{"/a/!{p;e ls\n}"}, []string{"ls"}, nil},
		{[]string{`\,a,p;w x`}, nil, []string{"x"}},
		{[]string{`s,a,w,;s/a/b/;y/ew/we/;a w text`}, nil, nil},
		{[]string{`\`}, nil, nil},
		{[]string{`s`}, nil, nil},
		{[]string{`y`}, nil, nil},
		{[]string{`s/a\/e/b/;1,3p`}, nil, nil},
		{[]string{`s/a/b/w x;p`}, nil, []string{"x;p"}},
	} {
		e, w := SedActions(tc.args)
		if !slices.Equal(e, tc.execs) || !slices.Equal(w, tc.writes) {
			t.Errorf("%q = %q, %q; want %q, %q", tc.args, e, w, tc.execs, tc.writes)
		}
	}
}
