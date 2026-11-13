package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/ai"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/nl"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// aiReview lets the model read the sandbox result and raise the risk. Any
// failure is shown but never blocks the command.
func aiReview(a *engine.Analysis, stderr io.Writer) {
	// Only commands that already need a decision are worth a model call: it can
	// take a second or more, and it can only raise a level that matters.
	if a.Level < domain.R2 || a.Level >= domain.R4 {
		return
	}
	b, err := backend()
	if err != nil {
		fmt.Fprintln(stderr, "AI review skipped:", err)
		return
	}
	if b == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	v, raised, err := ai.Review(ctx, b, *a)
	switch {
	case err != nil:
		fmt.Fprintln(stderr, "AI review unavailable:", firstErrLine(err))
	case raised:
		a.AddEffect(ai.Effect(b, v))
	}
}

func firstErrLine(err error) string {
	s, _, _ := strings.Cut(err.Error(), "\n")
	return s
}

// runExplain prints the analysis and a plain-English explanation.
func runExplain(args []string, stdout, stderr io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	b, err := backend()
	if err != nil || b == nil {
		fmt.Fprintln(stderr, "safeshell explain: AI is off. Type /models to choose a model, or run  safeshell ai setup")
		return 1
	}
	cwd, _ := os.Getwd()
	a := analyze(context.Background(), strings.Join(args, " "), cwd)
	printAnalysis(stdout, a, cwd, color, true)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	text, err := ai.Explain(ctx, b, a)
	if err != nil {
		fmt.Fprintln(stderr, "safeshell explain:", firstErrLine(err))
		return 1
	}
	fmt.Fprintf(stdout, "\nIn plain English\n  %s\n", report.Printable(text))
	return 0
}

// runAsk is `safeshell ask <english>`: translate, then treat the result like a typed command.
func runAsk(args []string, stdout, stderr io.Writer, color bool) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cwd, _ := os.Getwd()
	code, _ := askLine(strings.Join(args, " "), cwd, stdout, stderr, color)
	return code
}

// askLine turns a plain-English request into a command and runs it through
// the same guard as a typed one. The second result is false when no AI was
// available, so a caller that guessed this was English can let the line through.
func askLine(request, cwd string, stdout, stderr io.Writer, color bool) (code int, available bool) {
	b, err := backend()
	if err != nil || b == nil {
		fmt.Fprintln(stderr, "SafeShell AI is off. Type /models to choose a model, or run  safeshell ai setup")
		return 1, false
	}
	env := gatherEnv(cwd)
	var replies []string
	var in io.Reader
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second) // per call: typing time must not count
		r, err := nl.TranslateWith(ctx, b, request, replies, env)
		cancel()
		if err != nil {
			fmt.Fprintln(stderr, "safeshell ask:", firstErrLine(err))
			return 1, !strings.Contains(err.Error(), "safeshell ai setup") && !strings.Contains(err.Error(), "local engine")
		}
		if r.Question != "" {
			if in == nil {
				in = guardInput()
			}
			if len(replies) >= maxClarifications || !isTerminal(in) { // no one to ask: show it like an answer
				fmt.Fprintf(stderr, "The AI asks: %s\nNothing was run. Try again with more detail.\n", report.Printable(r.Question))
				return 1, true
			}
			fmt.Fprintf(stderr, "The AI asks: %s\n> ", report.Printable(r.Question))
			reply, err := readLine(in)
			if err != nil || reply == "" {
				fmt.Fprintln(stderr, "Cancelled. Nothing was run.")
				return 1, true
			}
			replies = append(replies, reply)
			continue
		}
		if r.Command == "" { // a question: show the answer, run nothing
			fmt.Fprintln(stderr, report.Printable(r.Answer))
			return 1, true
		}
		return proposeAndGuard(r, cwd, stdout, stderr, color), true
	}
}

// maxClarifications is how many times the model may ask before its question is just shown.
const maxClarifications = 2

// isTerminal is false for a pipe or file (a person cannot answer there). Any
// other reader, such as a test double, counts as answerable.
func isTerminal(in io.Reader) bool {
	f, ok := in.(*os.File)
	if !ok {
		return true
	}
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// maxReply caps a typed reply so an endless input cannot grow memory without bound.
const maxReply = 4096

// readLine reads one line a byte at a time so nothing meant for the guard's prompt is consumed.
func readLine(in io.Reader) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 1)
	for {
		n, err := in.Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				return strings.TrimSpace(sb.String()), nil
			}
			sb.WriteByte(buf[0])
			if sb.Len() >= maxReply {
				return strings.TrimSpace(sb.String()), nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) && sb.Len() > 0 {
				return strings.TrimSpace(sb.String()), nil
			}
			return "", err
		}
	}
}

func proposeAndGuard(r nl.Result, cwd string, stdout, stderr io.Writer, color bool) int {
	fmt.Fprintf(stderr, "Proposed: %s\n", report.Printable(r.Command))
	if r.Note != "" {
		fmt.Fprintf(stderr, "          %s\n", report.Printable(r.Note))
	}
	return guardLine(r.Command, cwd, stdout, stderr, color, true)
}
