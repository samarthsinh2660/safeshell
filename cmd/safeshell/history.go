package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/samarthsinh2660/safeshell/internal/audit"
)

const (
	historyDefaultLines = 20
	historyLineWidth    = 60
)

// runHistory prints the decisions SafeShell recorded, newest first.
func runHistory(args []string, stdout, stderr io.Writer) int {
	limit := historyDefaultLines
	asJSON := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			asJSON = true
		case "-n", "--lines":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "safeshell history: -n needs a number of lines")
				return 2
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				fmt.Fprintf(stderr, "safeshell history: invalid -n %q: want a positive number\n", args[i])
				return 2
			}
			limit = n
		default:
			fmt.Fprintf(stderr, "safeshell history: unknown option %q\n", args[i])
			return 2
		}
	}

	path := auditPath()
	entries, skipped, err := audit.Read(path)
	if err != nil {
		fmt.Fprintf(stderr, "safeshell history: could not read %s: %v\n", path, err)
		return 1
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "No decisions recorded yet.")
		if skipped > 0 {
			printSkipped(stderr, skipped)
		}
		return 0
	}

	// The log is append-only, so reversing it puts the latest decision first.
	newest := make([]audit.Entry, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		newest = append(newest, entries[i])
	}

	if len(newest) > limit {
		newest = newest[:limit]
	}

	if asJSON {
		if err := json.NewEncoder(stdout).Encode(newest); err != nil {
			fmt.Fprintf(stderr, "safeshell history: could not encode JSON: %v\n", err)
			return 1
		}
		if skipped > 0 {
			printSkipped(stderr, skipped)
		}
		return 0
	}

	fmt.Fprintf(stdout, "%-19s  %-5s  %-8s  %-9s  %s\n", "TIME", "LEVEL", "DECISION", "OUTCOME", "LINE")
	for _, e := range newest {
		fmt.Fprintf(stdout, "%-19s  %-5s  %-8s  %-9s  %s\n",
			e.Time.Local().Format("2006-01-02 15:04:05"), e.Level, e.Decision, e.Outcome, oneLine(e.Line))
	}
	if skipped > 0 {
		printSkipped(stderr, skipped)
	}
	return 0
}

func printSkipped(w io.Writer, count int) {
	unit := "lines"
	if count == 1 {
		unit = "line"
	}
	fmt.Fprintf(w, "%d %s in the log could not be read and were skipped.\n", count, unit)
}

// oneLine flattens a command for the table so a multi-line entry stays on one row,
// replaces unprintable control characters with visible escapes, and truncates cleanly by runes.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsPrint(r) {
			b.WriteRune(r)
		} else {
			q := strconv.QuoteRuneToASCII(r)
			b.WriteString(strings.Trim(q, "'"))
		}
	}
	runes := []rune(b.String())
	if len(runes) > historyLineWidth {
		return string(runes[:historyLineWidth-3]) + "..."
	}
	return string(runes)
}
