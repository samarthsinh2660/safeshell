package shellparse

import "strings"

// wrapped finds the command that su, runuser, pkexec, nsenter, watch or flock
// runs, as words ready to unwrap further. Code given to a shell (su -c, watch,
// flock -c) comes back as `sh -c CODE`. It is nil when no command is named, so
// the wrapper itself stays the command. su, runuser and pkexec run as root.
func wrapped(args []string, cmd *Command) []string {
	switch args[0] {
	case "su", "runuser":
		cmd.Sudo = true
		return suCommand(args)
	case "pkexec":
		cmd.Sudo = true
		return skipOpts(args[1:], "--user", "-u")
	case "nsenter":
		return skipOpts(args[1:], "-t", "--target", "-S", "--setuid", "-G", "--setgid")
	case "watch":
		return watchCommand(args)
	}
	return flockCommand(args)
}

func shCode(code string) []string { return []string{"sh", "-c", code} }

// suOpts are the su and runuser options that take a value of their own.
var suOpts = []string{"-s", "-g", "-G", "-w", "--shell", "--group", "--supp-group", "--whitelist-environment"}

// suCommand handles `su [-] [user] -c CODE` and `runuser -u USER [--] CMD`.
// The user may come before -c, so every word before `--` is read.
func suCommand(args []string) []string {
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			return nil
		case takesValue(a, "-c", "--command"):
			if i+1 < len(args) {
				return shCode(args[i+1])
			}
			return nil
		case strings.HasPrefix(a, "--command="):
			return shCode(strings.TrimPrefix(a, "--command="))
		case takesValue(a, "-u", "--user"): // runuser: the command follows the user
			return skipOpts(args[min(i+2, len(args)):], suOpts...)
		case takesValue(a, suOpts...):
			i++
		}
	}
	return nil
}

// watchCommand returns what watch runs. Unless -x is given, watch hands the
// words to `sh -c`.
func watchCommand(args []string) []string {
	rest := skipOpts(args[1:], "-n", "--interval")
	if len(rest) == 0 {
		return nil
	}
	for _, a := range args[1 : len(args)-len(rest)] {
		if a == "--exec" || strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "x") {
			return rest
		}
	}
	return shCode(strings.Join(rest, " "))
}

// flockCommand returns what flock runs: the words after its lock file, or the
// text of -c.
func flockCommand(args []string) []string {
	fileSeen := false
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case takesValue(a, "-c", "--command"):
			if i+1 < len(args) {
				return shCode(args[i+1])
			}
			return nil
		case strings.HasPrefix(a, "--command="):
			return shCode(strings.TrimPrefix(a, "--command="))
		case takesValue(a, "-w", "-E", "--timeout", "--wait", "--conflict-exit-code"):
			i++
		case strings.HasPrefix(a, "-") && a != "-":
		case !fileSeen:
			fileSeen = true
		default:
			return args[i:]
		}
	}
	return nil
}

// chrootCommand returns the words chroot runs after its new root folder.
func chrootCommand(args []string) []string {
	rest := skipOpts(args, "--userspec", "--groups")
	if len(rest) == 0 {
		return nil
	}
	return rest[1:]
}
