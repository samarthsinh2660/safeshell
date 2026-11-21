package overlayfs

import (
	"reflect"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func TestPickLooseHonoursBothLimits(t *testing.T) {
	files := []looseFile{{"/d/big", 500}, {"/d/c", 30}, {"/d/a", 30}, {"/d/b", 10}, {"/d/d", 60}}
	tests := []struct {
		name            string
		in              []looseFile
		perFile, budget int64
		chosen, skipped []string
	}{
		{"per-file limit", files, 100, 1000, []string{"/d/b", "/d/a", "/d/c", "/d/d"}, []string{"/d/big"}},
		{"total limit, smallest first", files, 100, 45, []string{"/d/b", "/d/a"}, []string{"/d/c", "/d/d", "/d/big"}},
		{"nothing fits", files, 5, 1000, nil, []string{"/d/b", "/d/a", "/d/c", "/d/d", "/d/big"}},
		{"no files", nil, 100, 100, nil, nil},
	}
	for _, tt := range tests {
		c, s := pickLoose(tt.in, tt.perFile, tt.budget)
		if !reflect.DeepEqual(paths(c), tt.chosen) || !reflect.DeepEqual(paths(s), tt.skipped) {
			t.Errorf("%s: chosen %v skipped %v", tt.name, paths(c), paths(s))
		}
	}
	if files[0].Path != "/d/big" {
		t.Error("the input must not be reordered")
	}
}

func paths(fs []looseFile) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

func TestDiffLoose(t *testing.T) {
	before := map[string]fileState{"/d/same": {Size: 1}, "/d/gone": {Size: 2}, "/d/edit": {Size: 3, Mtime: 1}}
	after := map[string]fileState{"/d/same": {Size: 1}, "/d/edit": {Size: 4, Mtime: 2}, "/d/new": {Size: 5}}
	want := []sandbox.Change{
		{Path: "d/edit", Kind: sandbox.Modified, Files: 1, Bytes: 4},
		{Path: "d/gone", Kind: sandbox.Deleted, Files: 1, Bytes: 2},
		{Path: "d/new", Kind: sandbox.Created, Files: 1, Bytes: 5},
	}
	if got := diffLoose(before, after); !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if got := diffLoose(before, before); len(got) != 0 {
		t.Errorf("no change, got %+v", got)
	}
}
