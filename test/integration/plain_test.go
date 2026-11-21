package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var colourCode = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestPlainBlockPromptInBash(t *testing.T) {
	needSandbox(t)
	victim := filepath.Join(t.TempDir(), "proj")
	_ = os.MkdirAll(victim, 0o755)
	_ = os.WriteFile(filepath.Join(victim, "a.txt"), []byte("x"), 0o644)

	sh := startBash(t, "SAFESHELL_PLAIN=1")
	sh.waitFor(t, "READY>")
	sh.send(`eval "$(safeshell hook bash)"` + "\r")
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "What do you want to do? ")
	sh.mu.Lock()
	shown := sh.buf.String()
	sh.mu.Unlock()
	for _, want := range []string{"Risk level 3 of 4: this cannot be undone.", "Type c to cancel.", "Type d to run it anyway."} {
		if !strings.Contains(shown, want) {
			t.Errorf("missing %q in:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "[c] cancel") || colourCode.MatchString(shown) { // bash's own bracketed-paste codes are not colour
		t.Errorf("plain mode showed the default prompt or colour:\n%s", shown)
	}

	// The same answers work: c cancels and nothing is deleted.
	sh.send("c\r")
	sh.waitFor(t, "Nothing was run")
	if _, err := os.Stat(filepath.Join(victim, "a.txt")); err != nil {
		t.Fatal("the dangerous command ran")
	}

	// d, then typed yes, still runs it.
	sh.send("rm -rf " + victim + "\r")
	sh.waitFor(t, "What do you want to do? ")
	sh.send("d\r")
	sh.waitFor(t, "Do you want to run it? ")
	sh.send("yes\r")
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(victim); err != nil {
			return
		}
	}
	t.Fatal("do-it-anyway did not run the command")
}
