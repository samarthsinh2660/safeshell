package voice

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Runner runs a program and returns what it printed to stdout.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// WhisperCLI transcribes with the whisper.cpp command-line program.
type WhisperCLI struct {
	Bin   string
	Model string
	Run   Runner // exec when nil
}

// Args is the command line that transcribes wav and writes outBase.txt.
func (w WhisperCLI) Args(wav, outBase string) []string {
	return []string{"-m", w.Model, "-f", wav, "-l", "en", "-nt", "-np", "-otxt", "-of", outBase}
}

func (w WhisperCLI) Transcribe(ctx context.Context, wav string) (string, error) {
	run := w.Run
	if run == nil {
		run = execRunner
	}
	base := filepath.Join(filepath.Dir(wav), "transcript")
	out, err := run(ctx, w.Bin, w.Args(wav, base)...)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("the speech engine failed: %w. Run  safeshell voice status  to check it", err)
	}
	text := string(out)
	if b, err := os.ReadFile(base + ".txt"); err == nil {
		text = string(b)
	}
	text = Clean(text)
	if text == "" {
		return "", ErrNoSpeech
	}
	return text, nil
}
