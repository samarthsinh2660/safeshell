package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/config"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/snapshot"
)

const (
	defaultKeepDays = 30
	defaultMaxGiB   = 5
)

// pruneLimits are the configured caps, or the defaults when the config cannot be read.
func pruneLimits() (time.Duration, int64) {
	days, gib := defaultKeepDays, defaultMaxGiB
	if c, err := config.Load(config.Path()); err == nil {
		days, gib = c.Snapshots.KeepDays, c.Snapshots.MaxGiB
	}
	return time.Duration(days) * 24 * time.Hour, int64(gib) << 30
}

// autoPrune trims old snapshots after a new one, never the new one. Failing
// to trim never matters to the command that is about to run.
func autoPrune(store, keep string) {
	age, max := pruneLimits()
	_, _ = snapshot.Prune(store, age, max, keep)
}

// parseAge reads "30d" or "12h".
func parseAge(s string) (time.Duration, bool) {
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'h': time.Hour}[last(s)]
	n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSuffix(s, "d"), "h"))
	return time.Duration(n) * unit, unit != 0 && err == nil && n > 0
}

// parseSize reads "5G", "500M", "2GiB" (binary units; no unit means bytes).
func parseSize(s string) (int64, bool) {
	s = strings.TrimSuffix(strings.TrimSuffix(strings.ToUpper(s), "B"), "I")
	mult := int64(1)
	if i := strings.IndexByte("KMGT", last(s)); i >= 0 && s != "" {
		mult, s = int64(1)<<(10*(i+1)), s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n * mult, err == nil && n > 0 && n*mult/mult == n
}

func last(s string) byte {
	if s == "" {
		return 0
	}
	return s[len(s)-1]
}

// runSnapshotsPrune deletes old snapshots: oldest first, until none is older
// than --older and the rest fit in --max. Without the options it uses the
// limits from the config.
func runSnapshotsPrune(args []string, stdout, stderr io.Writer) int {
	age, max := pruneLimits()
	for ; len(args) > 0; args = args[2:] {
		if (args[0] != "--older" && args[0] != "--max") || len(args) < 2 {
			fmt.Fprintln(stderr, "safeshell snapshots prune: use  safeshell snapshots prune [--older 30d] [--max 5G]")
			return 2
		}
		if args[0] == "--older" {
			a, ok := parseAge(args[1])
			if !ok {
				fmt.Fprintf(stderr, "safeshell snapshots prune: --older needs an age like 30d or 12h, not %q\n", args[1])
				return 2
			}
			age = a
		} else {
			m, ok := parseSize(args[1])
			if !ok {
				fmt.Fprintf(stderr, "safeshell snapshots prune: --max needs a size like 5G or 500M, not %q\n", args[1])
				return 2
			}
			max = m
		}
	}
	r, err := snapshot.Prune(snapshotDir(), age, max, "")
	if err != nil {
		fmt.Fprintln(stderr, "safeshell snapshots prune:", err)
		return 1
	}
	fmt.Fprintf(stdout, "Removed %d, freeing %s. %d left, %s.\n", r.Removed, report.HumanBytes(r.Freed), r.Left, report.HumanBytes(r.Size))
	return 0
}
