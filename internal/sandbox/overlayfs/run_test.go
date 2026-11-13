//go:build linux

package overlayfs

import (
	"os"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func TestCapBufferKeepsTheStartAndFlagsTheCut(t *testing.T) {
	c := &capBuffer{max: 5}
	n, err := c.Write([]byte("abc"))
	if n != 3 || err != nil || c.cut {
		t.Fatalf("first write: %d %v cut=%v", n, err, c.cut)
	}
	n, _ = c.Write([]byte("defgh"))
	if n != 5 || !c.cut || c.String() != "abcde" {
		t.Errorf("second write must report all bytes written, keep only the first 5: %d %q cut=%v", n, c.String(), c.cut)
	}
	if n, _ = c.Write([]byte("zzz")); n != 3 || c.String() != "abcde" {
		t.Errorf("writes after the limit are swallowed: %d %q", n, c.String())
	}
}

func TestCapBufferUnderTheLimitIsNotCut(t *testing.T) {
	c := &capBuffer{max: 10}
	c.Write([]byte("hi"))
	if c.cut || c.String() != "hi" {
		t.Errorf("%q cut=%v", c.String(), c.cut)
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("boom\nsecond"); got != "boom" {
		t.Errorf("got %q", got)
	}
	if got := firstLine("  \n"); !strings.Contains(got, "AppArmor") {
		t.Errorf("empty output should point at the likely cause, got %q", got)
	}
}

func TestDetail(t *testing.T) {
	if detail("  ") != "" || detail("x\n") != "\nx" {
		t.Error("detail formatting wrong")
	}
}

func TestSummaryCapsAndCountsPartialDirs(t *testing.T) {
	b := &builder{warnings: []sandbox.Warning{{Paths: []string{"/x"}, Text: "/x is not available"}}, partial: []string{"/a", "/b", "/c", "/d", "/e", "/f"}}
	got := b.summary()
	if len(got) != 2 || !strings.Contains(got[1].Text, "6 directories") || len(got[1].Paths) != 6 || strings.Contains(got[1].Text, "/e") {
		t.Errorf("summary = %q", got)
	}
	if len((&builder{}).summary()) != 0 {
		t.Error("nothing wrong, nothing to say")
	}
}

func TestScratchBase(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if got := scratchBase(); got != os.Getenv("XDG_RUNTIME_DIR") {
		t.Errorf("should prefer the runtime dir, got %q", got)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/definitely/not/here")
	if got := scratchBase(); got == "/definitely/not/here" {
		t.Error("a missing runtime dir must be skipped")
	}
}

func TestMarkWritableKeepsOnlyPathsTheUserCanWrite(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root can write everywhere")
	}
	open, shut := t.TempDir(), t.TempDir()
	if err := os.Chmod(shut, 0o555); err != nil {
		t.Fatal(err)
	}
	got := markWritable([]sandbox.Warning{{Paths: []string{open, shut}}, {Paths: []string{shut, "/definitely/not/here"}}})
	if len(got[0].Writable) != 1 || got[0].Writable[0] != open || len(got[1].Writable) != 0 {
		t.Errorf("writable = %q, %q", got[0].Writable, got[1].Writable)
	}
}
