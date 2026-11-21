package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/runtime"
	"github.com/samarthsinh2660/safeshell/internal/voice"
)

const voiceNotSetUp = "Voice is not set up yet. Run:  safeshell voice setup   (builds a small speech engine and downloads a speech model, 148 MB by default; everything stays on this machine)"

// voiceSetup says where voice files live and which tools exist; tests replace it.
var voiceSetup = func() voice.Setup {
	return voice.Setup{Dirs: runtime.DefaultDirs(), LookPath: exec.LookPath, RunTTY: voice.RunTTY}
}

// voiceChoice is voiceSetup() set to the speech model chosen in the config,
// with the language as saved (empty = the default). An unreadable config is an error.
func voiceChoice() (voice.Setup, string, error) {
	s := voiceSetup()
	c, err := config.Load(config.Path())
	if err != nil {
		return s, "", err
	}
	m, _ := voice.FindModel(c.Voice.Model) // Load already checked it
	s.Model = m
	return s, c.Voice.Language, nil
}

// newVoice returns the recorder and transcriber to use, or an error saying
// what to install or run first; tests replace it.
var newVoice = func() (voice.Recorder, voice.Transcriber, error) {
	s, lang, err := voiceChoice()
	if err != nil {
		return nil, nil, err
	}
	st := s.Status()
	if !st.Ready() {
		return nil, nil, errors.New(voiceNotSetUp)
	}
	rec, ok := voice.PickRecorder(s.LookPath)
	if !ok {
		return nil, nil, errors.New("No recorder found. Run  safeshell voice setup  to install one")
	}
	return rec, voice.WhisperCLI{Bin: st.EngineBin, Model: s.ModelPath(), Lang: s.Model.Language(lang)}, nil
}

// interruptContext is cancelled by Ctrl-C. It is a test seam: tests replace it.
var interruptContext = func() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

// voiceLimit is the recording length after which "Time is up" is shown. It is
// a test seam: tests replace it.
var voiceLimit = voice.MaxRecord

// runVoice is `safeshell voice [--text | setup | status]`.
func runVoice(args []string, stdout, stderr io.Writer, color bool) int {
	switch {
	case len(args) == 0:
		return voiceAsk(stdout, stderr, color)
	case len(args) == 1 && args[0] == "--text":
		text, err := voiceHear(stderr)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintln(stdout, text)
		return 0
	case len(args) >= 1 && args[0] == "setup":
		return voiceSetupRun(args[1:], stdout, stderr)
	case len(args) == 1 && args[0] == "status":
		return voiceStatus(stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return 2
}

// voiceHear records until Enter (or 30 s) and returns the transcript.
func voiceHear(stderr io.Writer) (string, error) {
	rec, tr, err := newVoice()
	if err != nil {
		return "", err
	}
	ctx, cancel := interruptContext()
	defer cancel()
	in := guardInput()
	stop := make(chan struct{})
	go func() { _, _ = readLine(in); close(stop) }()
	fmt.Fprintln(stderr, "Listening... press Enter when you are done (30 s max).")
	start := time.Now()
	wav, cleanup, err := rec.Record(ctx, stop)
	if err != nil {
		return "", voiceErr(err)
	}
	defer cleanup()
	select {
	case <-stop:
	default: // the pending read would eat the next answer, so wait for it
		if time.Since(start) >= voiceLimit {
			fmt.Fprintln(stderr, "Time is up. Press Enter to continue.")
		}
		select {
		case <-stop:
		case <-ctx.Done():
			return "", voiceErr(ctx.Err())
		}
	}
	fmt.Fprintln(stderr, "Transcribing...")
	tctx, tcancel := context.WithTimeout(ctx, 60*time.Second)
	defer tcancel()
	text, err := tr.Transcribe(tctx, wav)
	return text, voiceErr(err)
}

// voiceErr puts cancellation and timeouts in plain English.
func voiceErr(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return errors.New(cancelledMsg)
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("the speech engine took too long. Try a shorter request, or run  safeshell voice status: %w", err)
	}
	return err
}

// voiceAsk is `safeshell voice`: hear, confirm the words, then treat them like typed English.
func voiceAsk(stdout, stderr io.Writer, color bool) int {
	text, err := voiceHear(stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stderr, "You said: %q\n", text)
	fmt.Fprint(stderr, "[Enter] use it  [e] edit  [c] cancel ")
	in := guardInput()
	ans, err := readLine(in)
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		if err != nil { // end of input
			return voiceCancelled(stderr)
		}
	case "e":
		fmt.Fprint(stderr, "Type the request: ")
		edited, _ := readLine(in)
		if text = strings.TrimSpace(edited); text == "" {
			return voiceCancelled(stderr)
		}
	default:
		return voiceCancelled(stderr)
	}
	code, _ := askLine(text, cwdOr(""), stdout, stderr, color)
	return code
}

func voiceCancelled(stderr io.Writer) int {
	fmt.Fprintln(stderr, cancelledMsg)
	return 1
}

// voiceSetupArgs reads [--yes] [--model NAME] [--language CODE]. An empty
// model or language means "keep what is chosen".
func voiceSetupArgs(args []string) (yes bool, model, lang string, err error) {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--yes":
			yes = true
		case (a == "--model" || a == "--language") && i+1 < len(args):
			i++
			if a == "--model" {
				model = args[i]
			} else {
				lang = args[i]
			}
		default:
			return false, "", "", errors.New("bad option")
		}
	}
	return yes, model, lang, nil
}

func voiceSetupRun(args []string, stdout, stderr io.Writer) int {
	yes, model, lang, err := voiceSetupArgs(args)
	if err != nil {
		fmt.Fprint(stderr, usage)
		return 2
	}
	s, chosenLang, err := voiceChoice()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup:", err)
		return 1
	}
	if s.Model, lang, err = voiceSetupChoice(s.Model, chosenLang, model, lang); err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup:", err)
		return 1
	}
	s.Out = stdout
	ctx, cancel := interruptContext()
	defer cancel()
	ask := func(q string) (string, bool) {
		in := guardInput()
		if !isTerminal(in) {
			return "", false
		}
		fmt.Fprint(stdout, q)
		a, err := readLine(in)
		return a, err == nil // end of input is not a yes
	}
	if err := s.EnsureTools(ctx, yes, ask); err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup:", voiceErr(err))
		return 1
	}
	bin, err := s.EnsureEngine(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Speech engine ready: %s\n", bin)
	path, err := s.EnsureModel(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup:", err)
		return 1
	}
	if err := config.SetVoice(config.Path(), s.Model.Name, lang); err != nil {
		fmt.Fprintln(stderr, "safeshell voice setup: the model is ready but your choice could not be saved:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Speech model ready: %s (language: %s)\nTry it: press Ctrl-X then Ctrl-V in your terminal, or run  safeshell voice\n", path, s.Model.Language(lang))
	return 0
}

// voiceSetupChoice applies --model and --language to what is already chosen.
// It returns the model and the language to save ("" when the model is
// English-only or the language is the default).
func voiceSetupChoice(cur voice.Model, curLang, model, lang string) (voice.Model, string, error) {
	m := cur
	if model != "" {
		var err error
		if m, err = voice.FindModel(model); err != nil {
			return m, "", err
		}
	}
	if err := voice.CheckLanguage(lang); err != nil {
		return m, "", err
	}
	switch {
	case lang == "" && m.English:
		return m, "", nil
	case lang == "":
		return m, curLang, nil
	case m.English && lang != "en":
		return m, "", fmt.Errorf("the %s model only understands English. Use  --model base  or  --model small  for %q", m.Name, lang)
	}
	return m, lang, nil
}

func voiceStatus(w, stderr io.Writer) int {
	s, lang, err := voiceChoice()
	if err != nil {
		fmt.Fprintln(stderr, "safeshell voice status:", err)
		return 1
	}
	st := s.Status()
	if rec, ok := voice.PickRecorder(s.LookPath); ok {
		fmt.Fprintf(w, "recorder: %s\n", rec.Name)
	} else {
		fmt.Fprintln(w, "recorder: none found: safeshell voice setup installs one")
	}
	fmt.Fprintf(w, "engine:   %s\n", voiceEngineText(st))
	if st.ModelReady {
		fmt.Fprintf(w, "model:    %s, downloaded (%s)\n", s.Model.Name, s.ModelPath())
	} else {
		fmt.Fprintf(w, "model:    %s, not downloaded: safeshell voice setup --model %s\n", s.Model.Name, s.Model.Name)
	}
	fmt.Fprintf(w, "language: %s\n", s.Model.Language(lang))
	return 0
}

func voiceEngineText(st voice.Status) string {
	switch {
	case st.EngineFromPATH:
		return st.EngineBin + " (from PATH)"
	case st.EngineBin != "":
		return st.EngineBin
	}
	return "not set up: safeshell voice setup"
}

// doctorVoice prints the Voice section of safeshell doctor. cards is the text
// of /proc/asound/cards.
func doctorVoice(w io.Writer, s voice.Setup, run func(string, ...string) (string, error), cards string) {
	fmt.Fprintln(w, "\nVoice")
	rec, ok := voice.PickRecorder(s.LookPath)
	name := "none (safeshell voice setup installs one)"
	if ok {
		name = rec.Name
	}
	fmt.Fprintf(w, "  info     %-26s %s\n", "recorder", name)
	fmt.Fprintf(w, "  info     %-26s %s\n", "microphone", micText(s, run, cards))
	st := s.Status()
	fmt.Fprintf(w, "  info     %-26s %s\n", "speech engine", voiceEngineText(st))
	model := "not downloaded"
	if st.ModelReady {
		model = "downloaded"
	}
	fmt.Fprintf(w, "  info     %-26s %s\n", "speech model", model)
}

func micText(s voice.Setup, run func(string, ...string) (string, error), cards string) string {
	if _, err := s.LookPath("arecord"); err == nil {
		if out, err := run("arecord", "-l"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "card ") {
					return "found: " + strings.TrimSpace(l)
				}
			}
		}
	}
	if c := strings.TrimSpace(cards); c != "" && !strings.Contains(c, "no soundcards") {
		return "a sound card is present"
	}
	return "unknown"
}
