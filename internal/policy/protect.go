package policy

import (
	"fmt"
	"path"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

// ProtectOutcome is what the protected folders ask of one command line. The
// zero value asks nothing.
type ProtectOutcome struct {
	MinLevel domain.Level `json:"min_level"`
	Reason   string       `json:"reason,omitempty"` // names the folder that was hit
}

// Under reports whether p is dir or lies inside it. Both are absolute and
// compared as written after lexical cleaning (symlinks are not followed), at a
// folder boundary: /home/a/thesis2 is not under /home/a/thesis.
func Under(dir, p string) bool {
	dir, p = path.Clean(dir), path.Clean(p)
	if dir == "/" {
		return strings.HasPrefix(p, "/")
	}
	return p == dir || strings.HasPrefix(p, dir+"/")
}

// EvaluateProtected raises to R3 any command that changes one of the files or
// folders in changed (absolute paths) when it is inside a protected folder,
// or is a folder that holds one (removing or moving it takes the protected
// folder along). Reading is not a change, so it is never passed in here.
func EvaluateProtected(folders, changed []string) ProtectOutcome {
	for _, f := range folders {
		for _, p := range changed {
			if Under(f, p) || Under(p, f) {
				return ProtectOutcome{MinLevel: domain.R3, Reason: fmt.Sprintf("this folder is on your protected list: %s", path.Clean(f))}
			}
		}
	}
	return ProtectOutcome{}
}

// Apply returns the level and decision after the protected folders. It never lowers either.
func (o ProtectOutcome) Apply(level domain.Level, d domain.Decision) (domain.Level, domain.Decision) {
	level = max(level, o.MinLevel)
	return level, max(d, Decide(level, nil))
}
