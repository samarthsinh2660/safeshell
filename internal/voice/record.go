package voice

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// MaxRecord is the longest a single request can be.
const MaxRecord = 30 * time.Second

// stopGrace is how long a recorder gets to finish its file after SIGINT.
const stopGrace = 2 * time.Second

// recorderTools is the order in which recorders are tried.
var recorderTools = []string{"pw-record", "parec", "arecord", "ffmpeg"}

// ToolRecorder records with one command-line tool.
type ToolRecorder struct {
	Name string // pw-record, parec, arecord or ffmpeg
	Bin  string // path to run; Name when empty
	Max  time.Duration
}

// PickRecorder returns the first recorder found, in the order pw-record,
// parec, arecord, ffmpeg.
func PickRecorder(lookPath func(string) (string, error)) (ToolRecorder, bool) {
	for _, n := range recorderTools {
		if p, err := lookPath(n); err == nil {
			return ToolRecorder{Name: n, Bin: p}, true
		}
	}
	return ToolRecorder{}, false
}

// Args is the command line that writes 16 kHz mono 16-bit WAV to file.
func (r ToolRecorder) Args(file string) []string {
	switch r.Name {
	case "pw-record":
		return []string{"--rate", "16000", "--channels", "1", "--format", "s16", file}
	case "parec":
		return []string{"--rate=16000", "--channels=1", "--format=s16le", "--file-format=wav", file}
	case "arecord":
		return []string{"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "wav", file}
	case "ffmpeg":
		return []string{"-loglevel", "error", "-f", "pulse", "-i", "default", "-ac", "1", "-ar", "16000", "-y", file}
	}
	return nil
}

// checkHint is a command the person can run to test their microphone.
func (r ToolRecorder) checkHint() string {
	switch r.Name {
	case "pw-record":
		return "wpctl status"
	case "parec":
		return "pactl list short sources"
	case "arecord":
		return "arecord -l"
	}
	return "ffmpeg -sources pulse"
}

func (r ToolRecorder) Record(ctx context.Context, stop <-chan struct{}) (string, func(), error) {
	noop := func() {}
	dir, err := os.MkdirTemp(os.TempDir(), "safeshell-voice-")
	if err != nil {
		return "", noop, fmt.Errorf("cannot make a temporary folder for the recording: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	wav := filepath.Join(dir, "request.wav")
	f, err := os.OpenFile(wav, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		cleanup()
		return "", noop, fmt.Errorf("cannot create the recording file: %w", err)
	}
	f.Close()
	bin := r.Bin
	if bin == "" {
		bin = r.Name
	}
	cmd := exec.Command(bin, r.Args(wav)...)
	if err := cmd.Start(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("cannot start the recorder (%s): %w", r.Name, err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	limit := r.Max
	if limit == 0 {
		limit = MaxRecord
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	select {
	case <-stop:
	case <-ctx.Done():
	case <-timer.C:
	case <-done:
	}
	_ = cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-done:
	case <-time.After(stopGrace):
		_ = cmd.Process.Kill()
		<-done
	}
	if err := ctx.Err(); err != nil {
		cleanup()
		return "", noop, err
	}
	if !validWAV(wav) {
		cleanup()
		return "", noop, fmt.Errorf("The recorder (%s) did not produce audio. Check your microphone with:  %s", r.Name, r.checkHint())
	}
	return wav, cleanup, nil
}

// validWAV is true when the file starts with RIFF and holds more than a header.
func validWAV(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4)
	if _, err := f.Read(head); err != nil || !bytes.Equal(head, []byte("RIFF")) {
		return false
	}
	fi, err := f.Stat()
	return err == nil && fi.Size() > 44
}
