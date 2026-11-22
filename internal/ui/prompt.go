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
// end of input, cancels: the safe answer is the default. With offer, a plain
// confirmation also accepts "a": run it, and always run this exact command in
// this folder (always is then true).
func Choose(in *bufio.Reader, w io.Writer, line string, d domain.Decision, alts []domain.Alternative, offer bool) (cmd string, ok, always bool) {
	return chooseWith(in, w, line, d, alts, false, offer)
}

// ChoosePlain is Choose for screen readers: no brackets or symbols, one full
// sentence per line, the choices spelled out in words and the question last.
// It accepts exactly the same answers as Choose.
func ChoosePlain(in *bufio.Reader, w io.Writer, line string, d domain.Decision, alts []domain.Alternative, offer bool) (cmd string, ok, always bool) {
	return chooseWith(in, w, line, d, alts, true, offer)
}

func chooseWith(in *bufio.Reader, w io.Writer, line string, d domain.Decision, alts []domain.Alternative, plain, offer bool) (cmd string, ok, always bool) {
	cmd, ok = answer(in, w, line, d, alts, plain, offer, &always)
	return cmd, ok, always
}

func answer(in *bufio.Reader, w io.Writer, line string, d domain.Decision, alts []domain.Alternative, plain, offer bool, always *bool) (cmd string, ok bool) {
	ask := func(prompt string) string {
		fmt.Fprint(w, prompt)
		s, _ := in.ReadString('\n')
		return strings.TrimSpace(s)
	}
	switch d {
	case domain.Allow:
		return line, true
	case domain.Confirm:
		var a string
		switch {
		case plain && offer:
			fmt.Fprint(w, "\nType yes to run it anyway. Type always to run it anyway and not be asked again for this exact command in this folder. Press Enter to cancel.\n")
			a = ask("Do you want to run it anyway? ")
		case plain:
			fmt.Fprint(w, "\nType yes to run it anyway. Press Enter to cancel.\n")
			a = ask("Do you want to run it anyway? ")
		case offer:
			a = ask("\nRun it anyway? [y/N/a] (a = yes, and don't ask again for this exact command in this folder) ")
		default:
			a = ask("\nRun it anyway? [y/N] ")
		}
		switch strings.ToLower(a) {
		case "a", "always":
			*always = offer
			return line, offer
		case "y", "yes":
			return line, true
		}
		return "", false
	case domain.HardBlock:
		if plain {
			fmt.Fprintf(w, "\nThis can destroy this machine.\nTo run it anyway, type exactly: %s\nPress Enter to cancel.\n", HardPhrase)
			if ask("Do you want to run it anyway? ") == HardPhrase {
				return line, true
			}
			return "", false
		}
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
	prompt += " (default c): "
	if plain {
		fmt.Fprintln(w, "\nType c to cancel. Pressing Enter also cancels.")
		for i, alt := range alts {
			fmt.Fprintf(w, "Type %d to run the safer command: %s\n", i+1, alt.Command)
		}
		fmt.Fprintln(w, "Type d to run it anyway. You will be asked to type yes to confirm.")
		prompt = "What do you want to do? "
	}
	for {
		a := strings.ToLower(ask(prompt))
		switch {
		case a == "" || a == "c" || a == "cancel":
			return "", false
		case a == "d":
			confirm := "This was blocked as dangerous. Type yes to run it: "
			if plain {
				fmt.Fprintln(w, "This was blocked as dangerous.\nType yes to run it. Anything else cancels.")
				confirm = "Do you want to run it? "
			}
			if strings.ToLower(ask(confirm)) == "yes" {
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
	return proceed(in, w, enterRuns, false)
}

// ProceedPlain is Proceed for screen readers: full sentences, the question last.
func ProceedPlain(in *bufio.Reader, w io.Writer, enterRuns bool) bool {
	return proceed(in, w, enterRuns, true)
}

func proceed(in *bufio.Reader, w io.Writer, enterRuns, plain bool) bool {
	switch {
	case plain && enterRuns:
		fmt.Fprint(w, "\nPress Enter or type yes to run it. Type no to cancel.\nDo you want to run it? ")
	case plain:
		fmt.Fprint(w, "\nThis changes your shell itself. Type yes to run it. Press Enter to cancel.\nDo you want to run it? ")
	case enterRuns:
		fmt.Fprint(w, "\nRun it? [Y/n] ")
	default:
		fmt.Fprint(w, "\nThis changes your shell itself. Run it? [y/N] ")
	}
	s, err := in.ReadString('\n')
	s = strings.ToLower(strings.TrimSpace(s))
	return s == "y" || s == "yes" || (s == "" && err == nil && enterRuns)
}
