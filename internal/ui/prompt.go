// Package ui asks the person what to do with a command SafeShell stopped.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// HardPhrase must be typed in full to run a command rated catastrophic.
const HardPhrase = "destroy my data"

// Choose asks what to do about a command that was not allowed outright. It
// returns the command to run, or ok=false to cancel. Anything unclear, and
// end of input, cancels: the safe answer is the default.
func Choose(in *bufio.Reader, w io.Writer, line string, d domain.Decision, alts []domain.Alternative) (cmd string, ok bool) {
	ask := func(prompt string) string {
		fmt.Fprint(w, prompt)
		s, _ := in.ReadString('\n')
		return strings.TrimSpace(s)
	}
	switch d {
	case domain.Allow:
		return line, true
	case domain.Confirm:
		if a := strings.ToLower(ask("\nRun it anyway? [y/N] ")); a == "y" || a == "yes" {
			return line, true
		}
		return "", false
	case domain.HardBlock:
		fmt.Fprintf(w, "\nThis can destroy this machine. To run it anyway type exactly: %s\n", HardPhrase)
		if ask("> ") == HardPhrase {
			return line, true
		}
		return "", false
	}
	prompt := "\n[c] cancel  [d] do it anyway"
	if len(alts) > 0 {
		prompt += fmt.Sprintf("  [1-%d] run a safer option", len(alts))
	}
	for {
		a := strings.ToLower(ask(prompt + " (default c): "))
		switch {
		case a == "" || a == "c" || a == "cancel":
			return "", false
		case a == "d":
			if strings.ToLower(ask("This was blocked as dangerous. Type yes to run it: ")) == "yes" {
				return line, true
			}
			return "", false
		}
		if n, err := strconv.Atoi(a); err == nil && n >= 1 && n <= len(alts) {
			return alts[n-1].Command, true
		}
		fmt.Fprintln(w, "Please answer with one of the choices.")
		if _, err := in.Peek(1); err != nil { // input ended
			return "", false
		}
	}
}

// Proceed asks whether to run a command the assistant wrote. Enter means yes,
// because the person has just read it and it passed the safety checks, unless
// enterRuns is false (the command changes the shell itself), when Enter means no.
func Proceed(in *bufio.Reader, w io.Writer, enterRuns bool) bool {
	if enterRuns {
		fmt.Fprint(w, "\nRun it? [Y/n] ")
	} else {
		fmt.Fprint(w, "\nThis changes your shell itself. Run it? [y/N] ")
	}
	s, err := in.ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes" || (s == "" && err == nil && enterRuns)
}
