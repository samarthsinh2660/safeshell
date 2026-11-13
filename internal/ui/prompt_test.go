package ui

import (
	"bufio"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

var alts = []domain.Alternative{{Command: "trash proj"}, {Command: "mv proj /tmp/x"}}

func choose(input string, d domain.Decision) (string, bool) {
	var out strings.Builder
	return Choose(bufio.NewReader(strings.NewReader(input)), &out, "rm -rf proj", d, alts)
}

func TestChoose(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		d        domain.Decision
		want     string
		ok       bool
	}{
		{"allow runs", "", domain.Allow, "rm -rf proj", true},
		{"confirm yes", "y\n", domain.Confirm, "rm -rf proj", true},
		{"confirm default no", "\n", domain.Confirm, "", false},
		{"confirm eof", "", domain.Confirm, "", false},
		{"block default cancels", "\n", domain.Block, "", false},
		{"block cancel", "c\n", domain.Block, "", false},
		{"block alt 2", "2\n", domain.Block, "mv proj /tmp/x", true},
		{"block alt out of range then cancel", "9\nc\n", domain.Block, "", false},
		{"block do anyway needs yes", "d\nyes\n", domain.Block, "rm -rf proj", true},
		{"block do anyway declined", "d\nno\n", domain.Block, "", false},
		{"block garbage then eof", "zzz\n", domain.Block, "", false},
		{"hard needs phrase", "yes\n", domain.HardBlock, "", false},
		{"hard with phrase", HardPhrase + "\n", domain.HardBlock, "rm -rf proj", true},
	} {
		got, ok := choose(tc.in, tc.d)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got (%q,%v), want (%q,%v)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestProceedDefaultsToYesButNotOnEndOfInput(t *testing.T) {
	for in, want := range map[string]bool{"\n": true, "y\n": true, "YES\n": true, "n\n": false, "no\n": false, "x\n": false} {
		var out strings.Builder
		if got := Proceed(bufio.NewReader(strings.NewReader(in)), &out, true); got != want {
			t.Errorf("%q: %v", in, got)
		}
	}
	var out strings.Builder
	if Proceed(bufio.NewReader(strings.NewReader("")), &out, true) {
		t.Error("end of input must not run the command")
	}
}

func TestProceedCanDefaultToNo(t *testing.T) {
	for in, want := range map[string]bool{"\n": false, "": false, "y\n": true, "yes\n": true, "n\n": false} {
		var out strings.Builder
		if got := Proceed(bufio.NewReader(strings.NewReader(in)), &out, false); got != want {
			t.Errorf("%q: %v", in, got)
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("prompt should show the default: %q", out.String())
		}
	}
}
