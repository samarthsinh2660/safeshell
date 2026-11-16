package sandbox

import (
	"testing"
	"time"
)

func TestTotals(t *testing.T) {
	d := FileDiff{Changes: []Change{
		{Kind: Created, Files: 2}, {Kind: Modified, Files: 1}, {Kind: Deleted, Files: 10, Bytes: 100}, {Kind: Replaced, Files: 4, Bytes: 8},
	}}
	if got := d.Totals(); got != (Totals{Created: 2, Modified: 1, Deleted: 14, DeletedBytes: 108}) {
		t.Errorf("totals = %+v", got)
	}
}

func TestChangeAbsAddsTheLeadingSlash(t *testing.T) {
	if got := (Change{Path: "home/me/a.txt"}).Abs(); got != "/home/me/a.txt" {
		t.Error(got)
	}
}

func TestSpecDefaults(t *testing.T) {
	d := Spec{Line: "ls"}.Defaults()
	if d.Timeout != 30*time.Second || d.MaxOutput != 64<<10 || d.Line != "ls" {
		t.Errorf("%+v", d)
	}
	set := Spec{Timeout: time.Second, MaxOutput: 5}.Defaults()
	if set.Timeout != time.Second || set.MaxOutput != 5 {
		t.Errorf("values that were set must stay: %+v", set)
	}
}
