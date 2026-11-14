package runtime

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/models"
)

// status answers every request with code and body.
type status struct {
	code int
	body string
}

func (s status) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: s.code, Status: http.StatusText(s.code), Body: io.NopCloser(strings.NewReader(s.body))}, nil
}

func TestInstallEngineAndModelReportFailedDownloadsAndBadArchives(t *testing.T) {
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	body := []byte("not a tar.gz")
	b := Build{Variant: "cpu", Main: Asset{Name: "x.tar.gz", Size: int64(len(body)), SHA: sum(body)}}
	var out strings.Builder
	if err := d.InstallEngine(context.Background(), &http.Client{Transport: status{404, ""}}, b, &out); err == nil {
		t.Error("a failed download must be an error")
	}
	// The checksum matches but the archive is not one: nothing is installed.
	if err := d.InstallEngine(context.Background(), &http.Client{Transport: status{200, string(body)}}, b, &out); err == nil || d.EngineInstalled(b) {
		t.Errorf("a bad archive must be refused: %v", err)
	}
	m := models.Model{Title: "T", File: "m.gguf", URL: "https://example.invalid/m", Size: 4, SHA: sum([]byte("abcd"))}
	if err := d.InstallModel(context.Background(), &http.Client{Transport: status{404, ""}}, m, &out); err == nil {
		t.Error("a failed model download must be an error")
	}
}

func TestMachineProbesReadTheRealSystem(t *testing.T) {
	if !exists("/") || exists("/no/such/path/anywhere") {
		t.Error("exists")
	}
	if readFile("/no/such/path/anywhere") != "" {
		t.Error("readFile of a missing file must be empty")
	}
	if _, err := os.Stat("/proc/self"); err == nil && readFile("/proc/self/comm") == "" {
		t.Error("readFile of a real file")
	}
}

func TestOldOnAndOffGPUSettingsStillWork(t *testing.T) {
	machine(t, "", true)
	if got := variants(Candidates("amd64", "on")); got != "vulkan,cpu" {
		t.Errorf("on: %s", got)
	}
	if got := variants(Candidates("amd64", "off")); got != "cpu" {
		t.Errorf("off: %s", got)
	}
}

func TestAnEngineWithAGoneProcessIsNotRunning(t *testing.T) {
	d := Dirs{Data: t.TempDir(), State: t.TempDir()}
	if _, ok := d.Running(); ok {
		t.Error("no state file means not running")
	}
	if (state{PID: 1 << 30}).running() {
		t.Error("a process that does not exist is not running")
	}
}
