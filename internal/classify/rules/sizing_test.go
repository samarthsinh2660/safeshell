package rules

import (
	"slices"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func TestSizingPaths(t *testing.T) {
	tests := []struct {
		line    string
		unknown []string
		want    []string
	}{
		{"rm -rf big", nil, nil},
		{"rmdir d", nil, nil},
		{"ln -s a b", nil, nil},
		{"cp -r src /dst", nil, []string{"/w/src"}},
		{"cp a b /dst/", nil, []string{"/w/a", "/w/b"}},
		{"cp a", nil, nil},
		{"rsync -a src/ /dst/", nil, []string{"/w/src"}},
		{"mv a /other/", nil, []string{"/w/a"}},
		{"cp *.iso /dst/", nil, []string{"/w"}},
		{"cp ~/a /dst/", nil, []string{"/w"}},
		{"cp -t /dst a", nil, []string{"/w/a"}},
		{"cp -t /dst a b", nil, []string{"/w/a", "/w/b"}},
		{"cp -r --target-directory=/dst a b", nil, []string{"/w/a", "/w/b"}},
		{"mv --target-directory /dst a b", nil, []string{"/w/a", "/w/b"}},
		{"cp -t /dst", nil, nil},
		{"cp -T a /dst", nil, []string{"/w/a"}},
		{"ls *.c | xargs cp -t /dst", nil, []string{"/w"}},
		{"chmod -R 700 d", nil, []string{"/w/d"}},
		{"sed -i s/a/b/ f", nil, []string{"/w/f"}},
		{"echo hi > out.txt", nil, []string{"/w/out.txt"}},
		{"echo hi > /dev/null", nil, nil},
		{"make clean", []string{"make"}, []string{"/w"}},
		{"make clean", nil, nil},
		{"cd sub && make clean", []string{"make"}, []string{"/w/sub"}},
		{"cd $x && make clean", []string{"make"}, []string{"/w"}},
		{"cd sub && cp a /dst/", nil, []string{"/w/sub/a"}},
		{"find . -name '*.o' | xargs chmod 600", nil, []string{"/w"}},
		{"make out && cp b /dst/", []string{"make"}, []string{"/w", "/w/b"}},
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
		if got := SizingPaths(cmds, "/w", tc.unknown); !slices.Equal(got, tc.want) {
			t.Errorf("%q: got %v, want %v", tc.line, got, tc.want)
		}
	}
}
