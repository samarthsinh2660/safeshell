package shellparse

import "strings"

// SedActions scans the scripts of a sed command for the GNU commands that run
// shell code (`e`, `s///e`) and write files (`w`, `W`, `s///w`). An empty exec
// entry means the pattern space itself is run as a command.
func SedActions(args []string) (execs, writes []string) {
	var scripts []string
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-e" || a == "--expression":
			if i+1 < len(args) {
				scripts = append(scripts, args[i+1])
				i++
			}
			explicit = true
		case strings.HasPrefix(a, "--expression="):
			scripts = append(scripts, strings.TrimPrefix(a, "--expression="))
			explicit = true
		case strings.HasPrefix(a, "-e") && !strings.HasPrefix(a, "--"):
			scripts = append(scripts, a[2:])
			explicit = true
		case a == "-f" || a == "-l" || a == "--file" || a == "--line-length":
			i++
		case !strings.HasPrefix(a, "-") && !explicit:
			scripts = append(scripts, a)
			explicit = true
		}
	}
	for _, s := range scripts {
		e, w := sedScan(s)
		execs, writes = append(execs, e...), append(writes, w...)
	}
	return execs, writes
}

// sedScan walks one script command by command.
func sedScan(s string) (execs, writes []string) {
	rest := func(i int) (string, int) { // the text to the end of the line
		j := strings.IndexByte(s[i:], '\n')
		if j < 0 {
			return strings.TrimSpace(s[i:]), len(s)
		}
		return strings.TrimSpace(s[i : i+j]), i + j
	}
	// skipTo returns the index after the next unescaped d.
	skipTo := func(i int, d byte) int {
		for ; i < len(s) && s[i] != d; i++ {
			if s[i] == '\\' {
				i++
			}
		}
		return i + 1
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case strings.IndexByte(" \t\n;{}!,~+$0123456789", c) >= 0:
			i++
		case c == '/' || c == '\\': // an address /re/ or \cREc
			d := c
			if c == '\\' && i+1 < len(s) {
				i++
				d = s[i]
			}
			i = skipTo(i+1, d)
		case c == 's' && i+1 < len(s):
			d := s[i+1]
			i = skipTo(skipTo(i+2, d), d)
			for i < len(s) && s[i] != ';' && s[i] != '\n' && s[i] != '}' {
				switch s[i] {
				case 'e':
					execs = append(execs, "")
				case 'w':
					var f string
					f, i = rest(i + 1)
					writes = append(writes, f)
					continue
				}
				i++
			}
		case c == 'y' && i+1 < len(s):
			d := s[i+1]
			i = skipTo(skipTo(i+2, d), d)
		case c == 'e' || c == 'w' || c == 'W':
			var t string
			t, i = rest(i + 1)
			if c == 'e' {
				execs = append(execs, t)
			} else {
				writes = append(writes, t)
			}
		case strings.IndexByte("aicrR:btT", c) >= 0: // text or a file name to the end of the line
			_, i = rest(i)
		default: // p, d, n and the other one-letter commands
			i++
		}
	}
	return execs, writes
}
