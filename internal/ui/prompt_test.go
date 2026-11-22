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
	c, ok, _ := Choose(bufio.NewReader(strings.NewReader(input)), &out, "rm -rf proj", d, alts, false)
	return c, ok
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

func TestChoosePlainAcceptsTheSameAnswersAsChoose(t *testing.T) {
	for _, d := range []domain.Decision{domain.Confirm, domain.Block, domain.HardBlock} {
		for _, in := range []string{"", "\n", "y\n", "yes\n", "c\n", "1\n", "2\n", "9\nc\n", "d\nyes\n", "d\nno\n", "zzz\n", HardPhrase + "\n"} {
			var a, b strings.Builder
			wantCmd, wantOk, _ := Choose(bufio.NewReader(strings.NewReader(in)), &a, "rm -rf proj", d, alts, false)
			gotCmd, gotOk, _ := ChoosePlain(bufio.NewReader(strings.NewReader(in)), &b, "rm -rf proj", d, alts, false)
			if gotCmd != wantCmd || gotOk != wantOk {
				t.Errorf("%v %q: plain (%q,%v), default (%q,%v)", d, in, gotCmd, gotOk, wantCmd, wantOk)
			}
		}
	}
	if cmd, ok, _ := ChoosePlain(bufio.NewReader(strings.NewReader("")), &strings.Builder{}, "ls", domain.Allow, nil, false); cmd != "ls" || !ok {
		t.Error("an allowed command runs")
	}
}

func TestChoosePlainSpellsOutTheChoicesAndAsksLast(t *testing.T) {
	var out strings.Builder
	ChoosePlain(bufio.NewReader(strings.NewReader("c\n")), &out, "rm -rf proj", domain.Block, alts, false)
	for _, want := range []string{
		"Type c to cancel.", "Type 1 to run the safer command: trash proj\n", "Type 2 to run the safer command: mv proj /tmp/x\n",
		"Type d to run it anyway.", "\nWhat do you want to do? ",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in %q", want, out.String())
		}
	}
	if !strings.HasSuffix(out.String(), "What do you want to do? ") || strings.ContainsAny(out.String(), "[]") {
		t.Errorf("the question is last and there are no brackets: %q", out.String())
	}
	out.Reset()
	ChoosePlain(bufio.NewReader(strings.NewReader("d\nyes\n")), &out, "rm -rf proj", domain.Block, nil, false)
	if strings.Contains(out.String(), "safer command") || !strings.HasSuffix(out.String(), "Do you want to run it? ") {
		t.Errorf("%q", out.String())
	}
	out.Reset()
	ChoosePlain(bufio.NewReader(strings.NewReader("\n")), &out, "rm -rf /", domain.HardBlock, alts, false)
	if !strings.Contains(out.String(), "type exactly: "+HardPhrase+"\n") || !strings.HasSuffix(out.String(), "Do you want to run it anyway? ") {
		t.Errorf("%q", out.String())
	}
}

func TestProceedPlainTakesTheSameAnswers(t *testing.T) {
	for _, enterRuns := range []bool{true, false} {
		for _, in := range []string{"\n", "", "y\n", "yes\n", "n\n", "no\n", "x\n"} {
			var out strings.Builder
			want := Proceed(bufio.NewReader(strings.NewReader(in)), &strings.Builder{}, enterRuns)
			if got := ProceedPlain(bufio.NewReader(strings.NewReader(in)), &out, enterRuns); got != want {
				t.Errorf("%v %q: plain %v, default %v", enterRuns, in, got, want)
			}
			if !strings.HasSuffix(out.String(), "Do you want to run it? ") || strings.ContainsAny(out.String(), "[]") {
				t.Errorf("%q", out.String())
			}
		}
	}
}

func TestAlwaysIsOnlyAnAnswerWhenOffered(t *testing.T) {
	for _, tc := range []struct {
		name, in      string
		d             domain.Decision
		plain, offer  bool
		wantOk, wantA bool
		wantText      string
	}{
		{"a when offered", "a\n", domain.Confirm, false, true, true, true, "[y/N/a]"},
		{"always when offered", "ALWAYS\n", domain.Confirm, false, true, true, true, "ask again for this exact command"},
		{"plain always", "always\n", domain.Confirm, true, true, true, true, "Type always"},
		{"plain a", "a\n", domain.Confirm, true, true, true, true, "Type always"},
		{"a not offered cancels", "a\n", domain.Confirm, false, false, false, false, "[y/N] "},
		{"plain a not offered cancels", "a\n", domain.Confirm, true, false, false, false, "Type yes"},
		{"yes when offered is once", "y\n", domain.Confirm, false, true, true, false, ""},
		{"plain yes when offered is once", "yes\n", domain.Confirm, true, true, true, false, ""},
		{"enter cancels when offered", "\n", domain.Confirm, false, true, false, false, ""},
		{"a at a block is not an answer", "a\nc\n", domain.Block, false, true, false, false, ""},
		{"a at a hard block cancels", "a\n", domain.HardBlock, false, true, false, false, ""},
	} {
		var out strings.Builder
		in := bufio.NewReader(strings.NewReader(tc.in))
		choose := Choose
		if tc.plain {
			choose = ChoosePlain
		}
		cmd, ok, always := choose(in, &out, "make clean", tc.d, alts, tc.offer)
		if ok != tc.wantOk || always != tc.wantA || (ok && cmd != "make clean") {
			t.Errorf("%s: got (%q,%v,%v)", tc.name, cmd, ok, always)
		}
		if !strings.Contains(out.String(), tc.wantText) {
			t.Errorf("%s: prompt %q lacks %q", tc.name, out.String(), tc.wantText)
		}
	}
}
