package rules

import (
	"slices"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func TestWritePaths(t *testing.T) {
	tests := []struct {
		line     string
		want     []string
		complete bool
	}{
		{"rm notes.txt", []string{"/w/notes.txt"}, true},
		{"rm -rf /tmp/a b", []string{"/tmp/a", "/w/b"}, true},
		{"mv a b", []string{"/w/a", "/w/b => /w/b/a"}, true},
		{"mv a d/", []string{"/w/a", "/w/d/a"}, true},
		{"mv a b d", []string{"/w/a", "/w/b", "/w/d/a", "/w/d/b"}, true},
		{"mv a *.d", []string{"/w/a"}, false},
		{"mv -t dir a", nil, false},
		{"cp a b", []string{"/w/b => /w/b/a"}, true},
		{"cp -r src/ /srv/", []string{"/srv/src"}, true},
		{"cp --target-directory=d a", nil, false},
		{"cp", nil, true},
		{"sed -i s/a/b/ f g", []string{"/w/f", "/w/g"}, true},
		{"sed -i -e s/a/b/ f", []string{"/w/f"}, true},
		{"sed --expression=s/a/b/ -i f", []string{"/w/f"}, true},
		{"sed s/a/b/ f", nil, true},
		{"perl -pi -e s/a/b/ f", []string{"/w/f"}, true},
		{"perl -e print", nil, true},
		{"truncate -s 0 f", []string{"/w/f"}, true},
		{"shred f", []string{"/w/f"}, true},
		{"chmod -R 700 d", []string{"/w/d"}, true},
		{"chmod 700", nil, true},
		{"chown -R me d e", []string{"/w/d", "/w/e"}, true},
		{"echo hi > out.txt", []string{"/w/out.txt"}, true},
		{"echo hi >> /var/log/x 2> /dev/null", []string{"/var/log/x"}, true},
		{"echo hi | tee -a one two", []string{"/w/one", "/w/two"}, true},
		{"cd sub && rm f", []string{"/w/sub/f"}, true},
		{"cd /srv && rm f", []string{"/srv/f"}, true},
		{"rm *.txt", nil, false},
		{"rm $X", nil, false},
		{"rm ~/x", nil, false},
		{"find . -exec rm {} +", nil, false},
		{"ls | xargs rm", nil, false},
		{"ls | xargs echo", nil, true},
		{"ls -l", nil, true},
		{"frobnicate x", nil, true},
	}
	for _, tc := range tests {
		res, err := shellparse.Parse(tc.line)
		if err != nil {
			t.Fatalf("%q: %v", tc.line, err)
		}
		var cmds []shellparse.Command
		for _, p := range res.Pipelines {
			cmds = append(cmds, p.Cmds...)
		}
		ws, complete := WritePaths(cmds, "/w")
		var got []string
		for _, w := range ws {
			if w.Into != "" {
				w.Path += " => " + w.Into
			}
			got = append(got, w.Path)
		}
		if !slices.Equal(got, tc.want) || complete != tc.complete {
			t.Errorf("%q = %v, %v; want %v, %v", tc.line, got, complete, tc.want, tc.complete)
		}
	}
}

func TestWritePathsUnknownDirectory(t *testing.T) {
	got, complete := WritePaths([]shellparse.Command{{Name: "rm", Args: []string{"f"}, Cwd: "?"}}, "/w")
	if got != nil || complete {
		t.Errorf("%v %v", got, complete)
	}
}
