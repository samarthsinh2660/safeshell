package policy

import (
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// autorunPrograms are the everyday file and folder programs the assistant may
// run without a question. The list is short on purpose: a rating of R0 or R1
// says what the rules think a command does to files, not whether it runs other
// code, so interpreters, scripts, build tools, package managers, git, ssh and
// anything that can write a file through an option (sort -o, tree -o) are not
// on it, and a leading VAR=value is not either.
var autorunPrograms = []string{"cd", "ls", "pwd", "cat", "head", "tail", "wc", "du", "df", "stat", "file", "grep", "mv", "cp", "mkdir", "touch"}

// AutorunSafe reports whether every command in v is one of autorunPrograms. A
// verdict with no findings, or with a program the rules do not know, is not safe.
func AutorunSafe(v domain.Verdict) bool {
	if len(v.Findings) == 0 || len(v.Unclassified) > 0 {
		return false
	}
	for _, f := range v.Findings {
		w, _, _ := strings.Cut(strings.TrimSpace(f.Command), " ")
		if !slices.Contains(autorunPrograms, w) {
			return false
		}
	}
	return true
}
