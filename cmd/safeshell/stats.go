package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/samarthsinh2660/safeshell/internal/audit"
)

// stats are the counts taken from the audit log.
type stats struct {
	levels        [5]int // commands per risk level, R0 to R4
	blocks        int    // decision "block": refused unless overridden
	hardBlocks    int    // decision "hard-block"
	overrides     int    // a block that was run anyway
	hardOverrides int    // a hard block that was run anyway
	aiRaises      int
	failOpen      int // SafeShell failed and a line ran unchecked
	checksOff     int // SAFESHELL_OFF was used
}

// countLog adds up what the log says. Sandbox timeouts are not counted: the log
// does not record them.
func countLog(entries []audit.Entry) stats {
	var s stats
	for _, e := range entries {
		switch e.Outcome {
		case audit.OutcomeNotChecked:
			s.failOpen++
			continue
		case audit.OutcomeChecksOff:
			s.checksOff++
			continue
		}
		for i := range s.levels {
			if e.Level == fmt.Sprintf("R%d", i) {
				s.levels[i]++
			}
		}
		ran := e.Outcome == "ran"
		switch e.Decision {
		case "block":
			s.blocks++
			if ran {
				s.overrides++
			}
		case "hard-block":
			s.hardBlocks++
			if ran {
				s.hardOverrides++
			}
		}
		if e.AIRaised {
			s.aiRaises++
		}
	}
	return s
}

// runStats is `safeshell stats [--prometheus FILE]`.
func runStats(args []string, stdout, stderr io.Writer) int {
	prom := ""
	switch {
	case len(args) == 0:
	case len(args) == 2 && args[0] == "--prometheus":
		prom = args[1]
	default:
		fmt.Fprintln(stderr, "safeshell stats: use  safeshell stats  or  safeshell stats --prometheus FILE")
		return 2
	}
	path := auditPath()
	entries, skipped, err := audit.Read(path)
	if err != nil {
		fmt.Fprintf(stderr, "safeshell stats: could not read %s: %v\n", path, err)
		return 1
	}
	s := countLog(entries)
	if prom == "" {
		s.print(stdout, path)
	} else if err := writeAtomic(prom, s.prometheus()); err != nil {
		fmt.Fprintf(stderr, "safeshell stats: could not write %s: %v\nCheck that the folder exists and you can write to it.\n", prom, err)
		return 1
	}
	if skipped > 0 {
		printSkipped(stderr, skipped)
	}
	return 0
}

// print writes the short table people read.
func (s stats) print(w io.Writer, path string) {
	total := 0
	for _, n := range s.levels {
		total += n
	}
	fmt.Fprintf(w, "From %s (the current log only)\n\n", path)
	fmt.Fprintf(w, "%-28s %d\n", "Commands checked", total)
	for i, n := range s.levels {
		fmt.Fprintf(w, "  %-26s %d\n", fmt.Sprintf("R%d", i), n)
	}
	fmt.Fprintf(w, "%-28s %d\n", "Blocks", s.blocks)
	fmt.Fprintf(w, "%-28s %d\n", "Hard blocks", s.hardBlocks)
	fmt.Fprintf(w, "%-28s %d\n", "Blocks run anyway", s.overrides)
	fmt.Fprintf(w, "%-28s %d\n", "Hard blocks run anyway", s.hardOverrides)
	fmt.Fprintf(w, "%-28s %d\n", "Raised by the AI", s.aiRaises)
	fmt.Fprintf(w, "%-28s %d\n", "Failed, line not checked", s.failOpen)
	fmt.Fprintf(w, "%-28s %d\n", "Checks turned off", s.checksOff)
	fmt.Fprintf(w, "%-28s %s\n", "Sandbox timeouts", "not recorded")
}

// prometheus is the text exposition format, for node_exporter's textfile collector.
func (s stats) prometheus() []byte {
	var b []byte
	add := func(format string, a ...any) { b = fmt.Appendf(b, format+"\n", a...) }
	add("# HELP safeshell_commands_total Commands SafeShell checked, by risk level.")
	add("# TYPE safeshell_commands_total counter")
	for i, n := range s.levels {
		add(`safeshell_commands_total{level="R%d"} %d`, i, n)
	}
	add("# HELP safeshell_blocks_total Commands that were blocked, by kind of block.")
	add("# TYPE safeshell_blocks_total counter")
	add(`safeshell_blocks_total{decision="block"} %d`, s.blocks)
	add(`safeshell_blocks_total{decision="hard-block"} %d`, s.hardBlocks)
	add("# HELP safeshell_overrides_total Blocked commands that were run anyway, by kind of block.")
	add("# TYPE safeshell_overrides_total counter")
	add(`safeshell_overrides_total{decision="block"} %d`, s.overrides)
	add(`safeshell_overrides_total{decision="hard-block"} %d`, s.hardOverrides)
	add("# HELP safeshell_ai_raises_total Commands whose risk level the AI review raised.")
	add("# TYPE safeshell_ai_raises_total counter")
	add("safeshell_ai_raises_total %d", s.aiRaises)
	add("# HELP safeshell_events_total Times SafeShell did not check a line: it failed (fail_open) or checks were turned off (checks_off).")
	add("# TYPE safeshell_events_total counter")
	add(`safeshell_events_total{event="fail_open"} %d`, s.failOpen)
	add(`safeshell_events_total{event="checks_off"} %d`, s.checksOff)
	return b
}

// writeAtomic writes data to a temporary file next to path and renames it into
// place, so a reader never sees half a file. The file is world-readable because
// node_exporter runs as another user; it holds only counts.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // after a successful rename it is already gone
	_, werr := f.Write(data)
	err = errors.Join(werr, f.Close(), os.Chmod(tmp, 0o644))
	if err == nil {
		err = os.Rename(tmp, path)
	}
	return err
}
