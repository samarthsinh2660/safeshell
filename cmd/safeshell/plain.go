package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify"
	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/engine"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// plainSetting says whether the settings file asks for plain output; tests
// replace it so a developer's own settings never change what they see.
var plainSetting = readPlainSetting

// readPlainSetting reads the setting. A settings file that cannot be read
// counts as off: plain output only changes wording, so it must never get in the way.
func readPlainSetting() bool {
	c, err := config.Load(config.Path())
	return err == nil && c.Plain
}

// plainOn is true when output is for a screen reader: SAFESHELL_PLAIN=1 (which
// --plain sets for this run) or "plain": true in the settings file.
func plainOn() bool {
	return os.Getenv("SAFESHELL_PLAIN") == "1" || plainSetting()
}

// takePlain removes --plain from the leading options of check, analyze and
// guard and turns plain output on for this run. Like the other leading options
// it may come in any order; --cwd and the like are skipped with their value.
func takePlain(args []string) []string {
	for i := 0; i < len(args) && strings.HasPrefix(args[i], "-") && args[i] != "--"; i++ {
		switch args[i] {
		case "--plain":
			os.Setenv("SAFESHELL_PLAIN", "1")
			return append(args[:i:i], args[i+1:]...)
		case "--cwd", "--timeout", "--script", "--format", "--fail-on":
			i++ // skip the value
		}
	}
	return args
}

// plainLevel says a level in words: "Risk level 3 of 4: this cannot be undone."
func plainLevel(l domain.Level) string {
	return fmt.Sprintf("Risk level %d of 4: %s.", int(l), [...]string{
		"it only reads and changes nothing",
		"it makes changes that are easy to undo",
		"it changes things, and a snapshot can bring them back",
		"this cannot be undone",
		"this could destroy the machine or all your data",
	}[l])
}

var plainDecision = map[domain.Decision]string{
	domain.Allow:     "The command is allowed.",
	domain.Confirm:   "SafeShell will ask you before running the command.",
	domain.Block:     "The command is blocked, but you can choose to override that.",
	domain.HardBlock: "The command is hard blocked. Running it anyway needs a typed phrase.",
}

// sentence ends s with a full stop unless it already ends a sentence.
func sentence(s string) string {
	s = strings.TrimSpace(report.Printable(s))
	if s == "" || strings.ContainsAny(s[len(s)-1:], ".!?") {
		return s
	}
	return s + "."
}

// printPlainAnalysis is printAnalysis for screen readers: no colour or
// columns, one sentence per line.
func printPlainAnalysis(w io.Writer, a engine.Analysis, cwd string, verbose bool) {
	decision := plainDecision[a.Decision]
	if a.Team.Block != "" {
		decision = "The command is blocked."
	}
	fmt.Fprintln(w, plainLevel(a.Level), decision)
	if a.Team.Block != "" {
		fmt.Fprintln(w, "Blocked by your team's policy:", sentence(a.Team.Block))
	}
	fmt.Fprintln(w, "The command is:", report.Printable(a.Line))
	if a.Host.Reason != "" {
		fmt.Fprintln(w, "Raised because", sentence(a.Host.Reason))
	}
	if a.Protected.Reason != "" {
		fmt.Fprintln(w, "Raised because", sentence(a.Protected.Reason))
	}
	if a.Level > a.Verdict.Level && a.Level > max(a.Host.MinLevel, a.Protected.MinLevel) {
		fmt.Fprintf(w, "The command text alone looked like risk level %d of 4; what it would really do raised it to level %d of 4.\n", int(a.Verdict.Level), int(a.Level))
	}
	for _, f := range a.Verdict.Findings {
		if f.Level == domain.R0 && a.Verdict.Level > domain.R0 {
			continue
		}
		fmt.Fprintf(w, "Reason, risk level %d of 4: %s\n", int(f.Level), sentence(f.Reason))
	}
	printPlainObserved(w, a, cwd, verbose)
	printPlainAlternatives(w, a.Verdict.Alternatives)
}

func printPlainObserved(w io.Writer, a engine.Analysis, cwd string, verbose bool) {
	for _, e := range a.Effects {
		ev := strings.ToLower(e.Evidence.String())
		fmt.Fprintf(w, "%s evidence about %s: %s\n", strings.ToUpper(ev[:1])+ev[1:], e.Kind, sentence(e.Summary))
		for _, d := range e.Details {
			fmt.Fprintln(w, "Detail:", sentence(d))
		}
	}
	if a.SimNote != "" {
		fmt.Fprintln(w, sentence(a.SimNote))
	}
	if a.Sim != nil {
		shown, hidden := sandboxNotes(a.Sim.Warnings, a.Line, cwd, a.Sim.Diff.Changes, verbose)
		for _, wn := range shown {
			fmt.Fprintln(w, "Sandbox note:", sentence(wn.Text))
		}
		hiddenNotes(w, hidden, "", "")
	}
}

func printPlainAlternatives(w io.Writer, alts []domain.Alternative) {
	for i, a := range alts {
		fmt.Fprintf(w, "Safer option %d of %d: %s\n", i+1, len(alts), report.Printable(a.Command))
		fmt.Fprintln(w, "What it does:", sentence(a.Note))
	}
}

var plainLevelAction = [...]string{
	"The command is allowed.",
	"The command is allowed and SafeShell records how to undo it.",
	"SafeShell simulates the command first, then takes a snapshot.",
	plainDecision[domain.Block],
	plainDecision[domain.HardBlock],
}

// printPlainCheck is printCheck for screen readers.
func printPlainCheck(w io.Writer, line string, v domain.Verdict) {
	fmt.Fprintln(w, plainLevel(v.Level), plainLevelAction[v.Level])
	fmt.Fprintln(w, "The command is:", report.Printable(line))
	for _, f := range v.Findings {
		if f.Level == domain.R0 && v.Level > domain.R0 {
			continue
		}
		fmt.Fprintf(w, "Reason, risk level %d of 4: %s\n", int(f.Level), sentence(f.Reason))
		if f.Command != line {
			fmt.Fprintln(w, "It applies to:", report.Printable(f.Command))
		}
	}
	printPlainAlternatives(w, v.Alternatives)
	if len(v.Unclassified) > 0 {
		fmt.Fprintf(w, "Not recognised yet: %s These are rated risk level 1 of 4; run safeshell analyze to test them in a sandbox.\n", sentence(strings.Join(v.Unclassified, ", ")))
	}
}

// printPlainScriptTail is the end of printScriptAnalysis for screen readers.
func printPlainScriptTail(w io.Writer, lines []classify.LineFinding, a engine.Analysis, cwd string, verbose bool, level domain.Level) {
	if a.Level > classify.ScriptLevel(lines) {
		fmt.Fprintf(w, "The lines alone looked like risk level %d of 4; what the script would really do raised it to level %d of 4.\n", int(classify.ScriptLevel(lines)), int(a.Level))
	}
	printPlainObserved(w, a, cwd, verbose)
	decision := plainDecision[a.Decision]
	if a.Team.Block != "" {
		fmt.Fprintln(w, "Blocked by your team's policy:", sentence(a.Team.Block))
		decision = "The command is blocked."
	}
	fmt.Fprintln(w, plainLevel(level), decision)
}

// printPlainScriptLines is printScriptLines for screen readers.
func printPlainScriptLines(w io.Writer, lines []classify.LineFinding) {
	shown := false
	for _, lf := range lines {
		if lf.Level == domain.R0 {
			continue
		}
		shown = true
		fmt.Fprintf(w, "Line %d reads: %s\n", lf.Line, report.Printable(lf.Text))
		for _, reason := range lf.Reasons {
			fmt.Fprintf(w, "Line %d, risk level %d of 4: %s\n", lf.Line, int(lf.Level), sentence(reason))
		}
	}
	if !shown {
		fmt.Fprintln(w, "No risky commands found.")
	}
}
