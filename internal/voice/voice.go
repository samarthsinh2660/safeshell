// Package voice turns speech into text on this machine: it records from the
// microphone with whatever tool the system has and transcribes with a local
// whisper.cpp engine that SafeShell builds and manages itself. Nothing leaves
// the machine. The text then goes through the same path as typed English.
package voice

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// Recorder captures one spoken request. It returns the path of a 16 kHz mono
// WAV file and a cleanup that deletes it; recording ends when stop is closed,
// the context is cancelled, or the time limit passes. On error cleanup is a
// no-op and the files are already gone.
type Recorder interface {
	Record(ctx context.Context, stop <-chan struct{}) (wavPath string, cleanup func(), err error)
}

// Transcriber turns a WAV file into text.
type Transcriber interface {
	Transcribe(ctx context.Context, wavPath string) (string, error)
}

// ErrNoSpeech means the recording held no recognisable words.
var ErrNoSpeech = errors.New("I did not catch anything. Try again closer to the microphone.")

var tags = regexp.MustCompile(`\[[^\]]*\]|\([^)]*\)`)

// Clean tidies a transcript: it drops bracketed or parenthesised tags such as
// [BLANK_AUDIO] or (music), collapses whitespace and trims.
func Clean(text string) string {
	return strings.Join(strings.Fields(tags.ReplaceAllString(text, " ")), " ")
}
