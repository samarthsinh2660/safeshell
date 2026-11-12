package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

func TestSandboxNotesFollowSymlinks(t *testing.T) {
	d := t.TempDir()
	real := filepath.Join(d, "real")
	link := filepath.Join(d, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	ws := []sandbox.Warning{{Paths: []string{real}, Text: "real"}, {Paths: []string{"/boot"}, Text: "boot"}}
	shown, hidden := sandboxNotes(ws, "rm -rf "+link, "/work", nil, false)
	if len(shown) != 1 || shown[0].Text != "real" || hidden != 1 {
		t.Errorf("through a symlink: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf "+link, "/work", nil, true)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("verbose: shown %v, hidden %d", shown, hidden)
	}
	shown, hidden = sandboxNotes(ws, "rm -rf $X", "/work", nil, false)
	if len(shown) != 2 || hidden != 0 {
		t.Errorf("uncertain: shown %v, hidden %d", shown, hidden)
	}
}

func TestHiddenNotesWording(t *testing.T) {
	for n, want := range map[int]string{
		0: "",
		1: "1 sandbox note about other folders was hidden; add --verbose to see it.\n",
		3: "3 sandbox notes about other folders were hidden; add --verbose to see them.\n",
	} {
		var b bytes.Buffer
		hiddenNotes(&b, n, "", "")
		if b.String() != want {
			t.Errorf("%d: got %q", n, b.String())
		}
	}
}

func TestSimulateVerboseShowsEveryNote(t *testing.T) {
	fakeSim(t, sandbox.Result{Warnings: []sandbox.Warning{{Paths: []string{"/boot"}, Text: "boot note"}}}, nil)
	_, out, _ := runSS("simulate", "--cwd", "/work", "rm", "/work/x")
	if strings.Contains(out, "boot note") || !strings.Contains(out, "1 sandbox note about other folders was hidden") {
		t.Errorf("filtered:\n%s", out)
	}
	_, out, _ = runSS("simulate", "--verbose", "--cwd", "/work", "rm", "/work/x")
	if !strings.Contains(out, "Warning: boot note") || strings.Contains(out, "hidden") {
		t.Errorf("verbose:\n%s", out)
	}
}
