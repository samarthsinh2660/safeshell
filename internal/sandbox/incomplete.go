package sandbox

import "strings"

// overlayErrors are messages a command prints when the sandbox itself, not the
// command, could not do what it asked: the overlay refused (EOVERFLOW, EXDEV)
// or the scratch area was full (ENOSPC). Read-only errors are left out on
// purpose: a real read-only mount gives them too.
var overlayErrors = []string{
	"Value too large for defined data type",
	"Invalid cross-device link",
	"No space left on device",
}

// Incomplete returns why r cannot be trusted to show everything the command
// would do, or "" when it can. A full scratch area always counts; a sandbox
// error in the command's output counts only when the command failed.
func Incomplete(r Result) string {
	switch {
	case r.TimedOut:
		return "" // reported separately, with the changes seen before the timeout
	case r.ScratchFull:
		return "the sandbox ran out of space for the command's changes, so what it changes is not fully known. Check it yourself before you run it"
	case r.ExitCode == 0:
		return ""
	}
	for _, m := range overlayErrors {
		if strings.Contains(r.Stderr, m) {
			return "the sandbox could not finish this command (" + m + "), so what it changes is not known. Check it yourself before you run it"
		}
	}
	return ""
}
