package overlayfs

import "strings"

// rootCommands are the programs a script runs to get root. Inside the sandbox
// they cannot work (no new privileges), so each is replaced by a stand-in that
// notes the attempt and fails.
var rootCommands = []string{"sudo", "su", "pkexec", "doas", "runuser"}

// rootDirs are where those programs usually live.
var rootDirs = []string{"/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin", "/bin", "/sbin"}

// rootShim is the stand-in. It lives in the sandbox's own /run and records the
// name it was called by in the scratch folder, which the parent reads back.
const rootShim = `#!/bin/sh
n=${0##*/}
echo "$n" >> /run/safeshell/asks/root
echo "safeshell: $n is not available in the preview" >&2
exit 1
`

// maxAsksRead caps how much of the record is read back.
const maxAsksRead = 4096

// parseRootAsks returns the distinct root programs named in the record, in the
// order they first appear. Anything that is not one of rootCommands is ignored.
func parseRootAsks(record string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(record, "\n") {
		line = strings.TrimSpace(line)
		if seen[line] {
			continue
		}
		for _, c := range rootCommands {
			if line == c {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}
