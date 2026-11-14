package main

import (
	"fmt"
	"io"
	"strings"
)

// commandLineCmds take a command line, so only their leading options count.
var commandLineCmds = map[string]bool{"check": true, "analyze": true, "simulate": true, "explain": true, "ask": true, "guard": true}

// subcommands are the commands that answer -h and --help; a typo keeps its "unknown command" error.
var subcommands = map[string]bool{
	"check": true, "analyze": true, "simulate": true, "explain": true, "ask": true, "guard": true,
	"models": true, "ai": true, "voice": true, "learn": true, "deactivate": true, "activate": true, "undo": true, "snapshots": true, "daemon": true,
	"install": true, "uninstall": true, "hook": true, "version": true, "doctor": true, "tour": true, "help": true,
}

// wantsHelp is true when -h or --help is among the options of the subcommand
// cmd. Commands that take a command line (check, analyze, ...) only look at the
// leading options, so  safeshell check ls -h  still rates "ls -h". The options
// that take a value (--cwd, --timeout) must match parseFlags and runGuard.
func wantsHelp(cmd string, args []string) bool {
	free := commandLineCmds[cmd]
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			return true
		case free && (a == "--cwd" || a == "--timeout"):
			i++ // skip the value
		case free && (!strings.HasPrefix(a, "-") || a == "--"):
			return false
		}
	}
	return false
}

// subHelpTopic names the help topic that covers a subcommand, where one exists.
var subHelpTopic = map[string]string{
	"models": "models", "ai": "models", "voice": "voice", "undo": "undo",
	"snapshots": "undo", "install": "start", "ask": "english", "learn": "learn", "deactivate": "pause", "activate": "pause",
}

// runSubHelp prints the help for one subcommand and nothing else: its lines
// from the usage text, then its help topic when there is one.
func runSubHelp(cmd string, w io.Writer) int {
	if cmd == "help" {
		return runHelp(nil, w, w)
	}
	usageLines(cmd, w)
	if t, ok := subHelpTopic[cmd]; ok {
		fmt.Fprintln(w)
		return runHelp([]string{t}, w, w)
	}
	return 0
}

// usageLines prints the lines of the usage text that describe cmd.
func usageLines(cmd string, w io.Writer) {
	in, listed := false, false
	for _, l := range strings.SplitAfter(usage, "\n") {
		if listed && l == "\n" { // the command list ends at the first blank line
			break
		}
		listed = listed || strings.HasPrefix(l, "  safeshell ")
		if strings.HasPrefix(l, "  safeshell "+cmd+" ") {
			in = true
		} else if !strings.HasPrefix(l, "    ") {
			in = false
		}
		if in {
			fmt.Fprint(w, l)
		}
	}
}

// unexpectedArgs reports, for a subcommand that takes no options, what was
// passed and the next step. It returns false once it has printed.
func unexpectedArgs(cmd string, args []string, stderr io.Writer) bool {
	if len(args) == 0 {
		return true
	}
	fmt.Fprintf(stderr, "safeshell %s: unexpected %q. This command takes no options; run  safeshell %s --help\n", cmd, args[0], cmd)
	return false
}

// unknownOption reports a leading option the subcommand does not have. It
// returns false once it has printed.
func unknownOption(cmd string, args []string, stderr io.Writer) bool {
	if len(args) == 0 || !strings.HasPrefix(args[0], "-") {
		return true
	}
	fmt.Fprintf(stderr, "safeshell %s: unknown option %s; run  safeshell %s --help\n", cmd, args[0], cmd)
	return false
}

// endOptions ends the leading options: it drops one "--" so a command line
// that starts with a dash can follow, and otherwise reports an unknown option.
// ok is false once it has printed.
func endOptions(cmd string, args []string, stderr io.Writer) (rest []string, ok bool) {
	if len(args) > 0 && args[0] == "--" {
		return args[1:], true
	}
	return args, unknownOption(cmd, args, stderr)
}
