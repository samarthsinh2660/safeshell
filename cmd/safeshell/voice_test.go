package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
	"github.com/samarthsinh2660/safeshell/internal/voice"
)

type fakeRecorder struct{ err error }

func (f fakeRecorder) Record(_ context.Context, stop <-chan struct{}) (string, func(), error) {
	if f.err != nil {
		return "", func() {}, f.err
	}
	<-stop // like the real one: wait for Enter
	return "/tmp/fake.wav", func() {}, nil
}

type fakeTranscriber struct {
	text string
	err  error
}

func (f fakeTranscriber) Transcribe(context.Context, string) (string, error) { return f.text, f.err }

func useVoice(t *testing.T, r voice.Recorder, tr voice.Transcriber, err error) {
	t.Helper()
	old := newVoice
	newVoice = func() (voice.Recorder, voice.Transcriber, error) { return r, tr, err }
	t.Cleanup(func() { newVoice = old })
}

func TestVoiceTextPrintsOnlyTranscript(t *testing.T) {
	useVoice(t, fakeRecorder{}, fakeTranscriber{text: "show me the files"}, nil)
	typed(t, "\n")
	code, out, errOut := runSS("voice", "--text")
	if code != 0 || out != "show me the files\n" || !strings.Contains(errOut, "Listening...") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestVoiceTextErrors(t *testing.T) {
	useVoice(t, nil, nil, errors.New(voiceNotSetUp))
	code, out, errOut := runSS("voice", "--text")
	if code != 1 || out != "" || !strings.Contains(errOut, "safeshell voice setup") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
	useVoice(t, fakeRecorder{}, fakeTranscriber{err: voice.ErrNoSpeech}, nil)
	typed(t, "\n")
	code, out, errOut = runSS("voice", "--text")
	if code != 1 || out != "" || !strings.Contains(errOut, "I did not catch anything") {
		t.Errorf("%d %q %q", code, out, errOut)
	}
}

func TestVoiceNotSetUp(t *testing.T) {
	old := voiceSetup
	t.Cleanup(func() { voiceSetup = old })
	voiceSetup = func() voice.Setup {
		return voice.Setup{Dirs: runtime.Dirs{Data: t.TempDir()}, LookPath: func(string) (string, error) { return "", errors.New("no") }}
	}
	code, _, errOut := runSS("voice")
	if code != 1 || !strings.Contains(errOut, "Voice is not set up yet. Run:  safeshell voice setup") {
		t.Errorf("%d %q", code, errOut)
	}
}

func allowAnalysis(t *testing.T) *[2]string {
	a := sampleAnalysis()
	a.Level, a.Decision = domain.R0, domain.Allow
	return fakeAnalyze(t, a)
}

func TestVoiceConfirmEnterRunsThroughAskPath(t *testing.T) {
	useVoice(t, fakeRecorder{}, fakeTranscriber{text: "show all files"}, nil)
	got := allowAnalysis(t)
	useAI(t, fakeAI{reply: `{"command":"ls -la","note":"lists everything"}`}, nil)
	typed(t, "\n\n\n") // stop recording, accept the words, accept the command
	code, out, errOut := runSS("voice")
	if code != 0 || out != "ls -la\n" || got[0] != "ls -la" {
		t.Errorf("%d %q %q %v", code, out, errOut, got)
	}
	if !strings.Contains(errOut, `You said: "show all files"`) || !strings.Contains(errOut, "[Enter] use it  [e] edit  [c] cancel") || !strings.Contains(errOut, "Proposed: ls -la") {
		t.Errorf("%q", errOut)
	}
}

func TestVoiceConfirmEditUsesReplacement(t *testing.T) {
	useVoice(t, fakeRecorder{}, fakeTranscriber{text: "show all fines"}, nil)
	allowAnalysis(t)
	var asked string
	useAI(t, askRecorder{&asked}, nil)
	typed(t, "\ne\nshow all files\n\n")
	code, out, _ := runSS("voice")
	if code != 0 || out != "ls\n" || !strings.Contains(asked, "show all files") || strings.Contains(asked, "fines") {
		t.Errorf("%d %q asked=%q", code, out, asked)
	}
}

type askRecorder struct{ got *string }

func (askRecorder) Name() string { return "fake" }
func (a askRecorder) Complete(_ context.Context, _, user string) (string, error) {
	*a.got = user
	return `{"command":"ls"}`, nil
}

func TestVoiceConfirmCancel(t *testing.T) {
	for name, keys := range map[string]string{"c": "\nc\n", "end of input": "\n", "something else": "\nx\n", "empty edit": "\ne\n\n"} {
		useVoice(t, fakeRecorder{}, fakeTranscriber{text: "delete everything"}, nil)
		allowAnalysis(t)
		useAI(t, fakeAI{reply: `{"command":"rm -rf /"}`}, nil)
		typed(t, keys)
		code, out, errOut := runSS("voice")
		if code != 1 || out != "" || !strings.Contains(errOut, "Cancelled. Nothing was run.") || strings.Contains(errOut, "Proposed") {
			t.Errorf("%s: %d %q %q", name, code, out, errOut)
		}
	}
}

func TestVoiceBadArgs(t *testing.T) {
	if code, _, _ := runSS("voice", "shout"); code != 2 {
		t.Errorf("%d", code)
	}
}

func fakeSetup(t *testing.T, have ...string) voice.Setup {
	t.Helper()
	return voice.Setup{Dirs: runtime.Dirs{Data: t.TempDir()}, LookPath: func(n string) (string, error) {
		for _, h := range have {
			if h == n {
				return "/usr/bin/" + n, nil
			}
		}
		return "", errors.New("no")
	}}
}

func useSetup(t *testing.T, s voice.Setup) {
	t.Helper()
	old := voiceSetup
	t.Cleanup(func() { voiceSetup = old })
	voiceSetup = func() voice.Setup { return s }
}

func TestVoiceStatus(t *testing.T) {
	useSetup(t, fakeSetup(t))
	_, out, _ := runSS("voice", "status")
	for _, want := range []string{"recorder: none found: install pipewire-bin, pulseaudio-utils or alsa-utils", "engine:   not set up: safeshell voice setup", "model:    not downloaded"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	useSetup(t, fakeSetup(t, "arecord", "whisper-cli"))
	_, out, _ = runSS("voice", "status")
	if !strings.Contains(out, "recorder: arecord") || !strings.Contains(out, "/usr/bin/whisper-cli (from PATH)") {
		t.Errorf("%q", out)
	}
}

func TestVoiceSetupNeedsBuildTools(t *testing.T) {
	useSetup(t, fakeSetup(t))
	code, _, errOut := runSS("voice", "setup")
	if code != 1 || !strings.Contains(errOut, "needs cmake and a C++ compiler") {
		t.Errorf("%d %q", code, errOut)
	}
}

func TestDoctorVoiceRows(t *testing.T) {
	var b strings.Builder
	run := func(name string, args ...string) (string, error) {
		return "**** List of CAPTURE Hardware Devices ****\ncard 1: PCH [HDA Intel PCH], device 0: ALC [ALC]\n", nil
	}
	doctorVoice(&b, fakeSetup(t, "pw-record", "arecord"), run, "")
	out := b.String()
	for _, want := range []string{"Voice", "recorder", "pw-record", "found: card 1: PCH", "not set up: safeshell voice setup", "not downloaded"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %q", want, out)
		}
	}
	b.Reset()
	s := fakeSetup(t)
	_ = os.MkdirAll(filepath.Dir(s.ModelPath()), 0o755)
	f, _ := os.Create(s.ModelPath())
	_ = f.Truncate(147964211)
	f.Close()
	doctorVoice(&b, s, run, " 0 [PCH            ]: HDA-Intel\n")
	out = b.String()
	if !strings.Contains(out, "none (install pipewire-bin") || !strings.Contains(out, "a sound card is present") || !strings.Contains(out, "downloaded") || strings.Contains(out, "not downloaded") {
		t.Errorf("%q", out)
	}
	b.Reset()
	doctorVoice(&b, fakeSetup(t), run, "--- no soundcards ---\n")
	if !strings.Contains(b.String(), "unknown") {
		t.Errorf("%q", b.String())
	}
}

func TestVoiceContextErrorsArePlainEnglish(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"cancelled": {context.Canceled, "Cancelled. Nothing was run."},
		"too slow":  {context.DeadlineExceeded, "the speech engine took too long. Try a shorter request, or run  safeshell voice status"},
	} {
		useVoice(t, fakeRecorder{}, fakeTranscriber{err: tc.err}, nil)
		typed(t, "\n")
		code, out, errOut := runSS("voice", "--text")
		if code != 1 || out != "" || !strings.Contains(errOut, tc.want) {
			t.Errorf("%s: %d %q %q", name, code, out, errOut)
		}
	}
	if err := voiceErr(context.DeadlineExceeded); !errors.Is(err, context.DeadlineExceeded) {
		t.Error("the timeout must stay wrapped")
	}
}

func TestVoiceNoTimeUpMessageWhenRecorderEndsEarly(t *testing.T) {
	// The recorder returns at once, long before the limit; Enter arrives later.
	useVoice(t, instantRecorder{}, fakeTranscriber{text: "x"}, nil)
	pr, pw := io.Pipe()
	old := guardInput
	t.Cleanup(func() { guardInput = old; pw.Close() })
	guardInput = func() io.Reader { return pr }
	done := make(chan string, 1)
	go func() { _, _, e := runSS("voice", "--text"); done <- e }()
	_, _ = pw.Write([]byte("\n"))
	if e := <-done; strings.Contains(e, "Time is up") {
		t.Errorf("%q", e)
	}
}

type instantRecorder struct{}

func (instantRecorder) Record(context.Context, <-chan struct{}) (string, func(), error) {
	return "/tmp/fake.wav", func() {}, nil
}
