package nl

import (
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const shellName = `(?:ba|z|da|k)?sh`

// A download run straight in a shell: "curl ... | sh", "sh <(curl ...)" or "sh -c "$(curl ...)"".
var (
	fetchPipe  = regexp.MustCompile(`\b(?:curl|wget)\b[^|]*\|\s*(?:sudo\s+(?:-\S+\s+)*)?(` + shellName + `)\b`)
	fetchSubst = regexp.MustCompile(`\b(` + shellName + `)\s+(?:-c\s+["']?\$|<)\(\s*(?:curl|wget)\b`)
	fetchURL   = regexp.MustCompile(`'(https?://[^']+)'|"(https?://[^"]+)"|(https?://[^\s"'|)<>;&]+)`)
	unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

// reviewBeforeRun replaces a command that runs a download unread with one that
// saves the script and shows it. Other commands, and downloads whose address
// cannot be found, come back unchanged with an empty note.
func reviewBeforeRun(cmd string) (string, string) {
	m := fetchPipe.FindStringSubmatch(cmd)
	if m == nil {
		m = fetchSubst.FindStringSubmatch(cmd)
	}
	u := fetchURL.FindStringSubmatch(cmd)
	if m == nil || u == nil {
		return cmd, ""
	}
	addr := u[1] + u[2] + u[3]
	if strings.ContainsRune(addr, '\'') { // it goes inside '...' below; a quote would end it early
		return cmd, ""
	}
	name := "install.sh"
	if p, err := url.Parse(addr); err == nil {
		if n := unsafeName.ReplaceAllString(path.Base(p.Path), ""); strings.Trim(n, ".-") != "" && !strings.HasPrefix(n, "-") && !strings.HasPrefix(n, ".") { // never a dotfile such as .bashrc
			name = n
		}
	}
	return fmt.Sprintf("curl -fsSL '%s' -o %s && less %s", addr, name, name),
		fmt.Sprintf("Saves the script to %s and shows it instead of running it unread. If it looks right, run: %s %s", name, m[1], name)
}
