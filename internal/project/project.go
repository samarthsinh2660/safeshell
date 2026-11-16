// Package project reads a few cheap facts about a working directory (git
// branch, build files, script names) for the assistant. It only reads small
// files: it runs nothing and uses no network.
package project

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const maxRead = 64 << 10

func readSmall(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, maxRead))
	return string(b)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Facts describes dir, one short line per fact. It is empty when nothing is recognised.
func Facts(dir string) []string {
	var out []string
	if b := branch(dir); b != "" {
		out = append(out, "git repository, "+b)
	}
	if exists(filepath.Join(dir, "go.mod")) {
		out = append(out, "Go module (go.mod)")
	}
	if names := scripts(filepath.Join(dir, "package.json")); names != "" {
		out = append(out, "Node project (package.json), scripts: "+names)
	}
	for _, f := range []string{"pyproject.toml", "requirements.txt", "Cargo.toml", "Dockerfile"} {
		if exists(filepath.Join(dir, f)) {
			out = append(out, f+" present")
		}
	}
	if t := targets(filepath.Join(dir, "Makefile")); t != "" {
		out = append(out, "Makefile targets: "+t)
	}
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if exists(filepath.Join(dir, f)) {
			out = append(out, "Docker Compose file: "+f)
			break
		}
	}
	return out
}

// branch finds the repository containing dir by reading its HEAD file.
func branch(dir string) string {
	for d := dir; ; d = filepath.Dir(d) {
		g := filepath.Join(d, ".git")
		if fi, err := os.Stat(g); err == nil {
			if !fi.IsDir() { // a worktree or submodule: .git is a file naming the real folder
				rest, ok := strings.CutPrefix(strings.TrimSpace(readSmall(g)), "gitdir: ")
				if !ok {
					return "branch unknown"
				}
				if !filepath.IsAbs(rest) {
					rest = filepath.Join(d, rest)
				}
				g = rest
			}
			if ref, ok := strings.CutPrefix(strings.TrimSpace(readSmall(filepath.Join(g, "HEAD"))), "ref: refs/heads/"); ok {
				return "branch " + ref
			}
			return "detached HEAD"
		}
		if d == filepath.Dir(d) {
			return ""
		}
	}
}

// scripts lists the script names in a package.json, sorted and bounded.
func scripts(path string) string {
	var p struct{ Scripts map[string]json.RawMessage }
	if json.Unmarshal([]byte(readSmall(path)), &p) != nil {
		return ""
	}
	var names []string
	for n := range p.Scripts {
		names = append(names, n)
	}
	sort.Strings(names)
	return join(names, 12)
}

var target = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9_.-]*):($|[^=])`)

// targets lists the rule names in a Makefile in file order, bounded.
func targets(path string) string {
	var names []string
	seen := map[string]bool{}
	for _, l := range strings.Split(readSmall(path), "\n") {
		if m := target.FindStringSubmatch(l); m != nil && !seen[m[1]] {
			seen[m[1]] = true
			names = append(names, m[1])
		}
	}
	return join(names, 15)
}

func join(names []string, max int) string {
	if len(names) > max {
		names = append(names[:max], "...")
	}
	return strings.Join(names, " ")
}
