// Package archtest holds the test that keeps the hexagonal rules in
// doc/architecture.md true. It only reads the Go source files (go/parser), so
// it also runs in the Debian build, which has no go tool inside the test.
package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const module = "github.com/samarthsinh2660/safeshell/"

// scanned are the folders whose non-test .go files are checked.
var scanned = []string{"internal", "cmd", "eval"}

// pure packages do no file, process, network or environment access, so they
// may import only each other. shellparse and sandbox are pure too, because a
// pure package that imports an impure one is impure: shellparse only parses
// text, and sandbox holds the data types and parsers (the code that reads the
// overlay upper directory is in sandbox/overlayfs).
var pure = map[string]bool{
	"internal/domain": true, "internal/classify": true, "internal/classify/rules": true,
	"internal/policy": true, "internal/report": true, "internal/shellparse": true,
	"internal/sandbox": true,
}

// forbidden standard-library packages reach the machine. Pure string and
// data packages (path, path/filepath, net/netip, encoding/json, time types)
// are fine.
var forbidden = map[string]string{
	"os":        "reads files or the environment",
	"os/exec":   "starts processes",
	"os/signal": "handles process signals",
	"os/user":   "reads the account database",
	"net":       "uses the network",
	"net/http":  "uses the network",
	"syscall":   "calls the kernel",
	"io/ioutil": "reads and writes files",
	"plugin":    "loads code at run time",
}

// thirdParty is the only non-standard-library code a pure package may use.
const thirdParty = "mvdan.cc/sh/v3/"

// composition is the only package that may import the engine.
const composition = "cmd/safeshell"

// check returns one plain-English line per broken rule in the Go files of
// fsys.
func check(fsys fs.FS) (problems []string) {
	for _, root := range scanned {
		_ = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			src, err := fs.ReadFile(fsys, p)
			var f *ast.File
			if err == nil {
				f, err = parser.ParseFile(token.NewFileSet(), p, src, parser.SkipObjectResolution)
			}
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: cannot be read or parsed (%v)", p, err))
				return nil
			}
			problems = append(problems, checkFile(path.Dir(p), p, f)...)
			return nil
		})
	}
	sort.Strings(problems)
	return problems
}

func checkFile(pkg, file string, f *ast.File) []string {
	var out []string
	add := func(imp, format string, a ...any) {
		out = append(out, fmt.Sprintf("%s imports %q: %s", file, imp, fmt.Sprintf(format, a...)))
	}
	timeName := ""
	for _, spec := range f.Imports {
		imp, _ := strconv.Unquote(spec.Path.Value)
		inModule := strings.HasPrefix(imp, module)
		local := strings.TrimPrefix(imp, module)
		if imp == "time" {
			timeName = "time"
			if spec.Name != nil {
				timeName = spec.Name.Name
			}
		}
		switch {
		case inModule && local == "internal/engine" && pkg != "internal/engine" && pkg != composition:
			add(imp, "only cmd/safeshell may import the engine; adapters and other packages implement its interfaces and never import it")
		case inModule && pkg == "internal/engine" && !pure[local]:
			add(imp, "the engine may import only pure packages and reaches everything else through interfaces it declares itself")
		case inModule && pure[pkg] && !pure[local]:
			add(imp, "pure package %s may import only other pure packages (%s is not one)", pkg, local)
		case !pure[pkg] && pkg != "internal/engine":
			// adapters and the composition root may import anything else
		default:
			if why, bad := forbidden[imp]; bad {
				add(imp, "%s must not use this package (it %s); put that work in an adapter behind an interface", pkg, why)
			} else if !inModule && strings.Contains(strings.SplitN(imp, "/", 2)[0], ".") && !strings.HasPrefix(imp, thirdParty) {
				add(imp, "%s may import only the standard library and the shell parser library", pkg)
			}
		}
	}
	if pure[pkg] && timeName != "" {
		ast.Inspect(f, func(n ast.Node) bool {
			if s, ok := n.(*ast.SelectorExpr); ok && s.Sel.Name == "Now" {
				if id, ok := s.X.(*ast.Ident); ok && id.Name == timeName {
					out = append(out, fmt.Sprintf("%s uses time.Now: pure package %s must be given the time instead of reading the clock", file, pkg))
				}
			}
			return true
		})
	}
	return out
}

func TestArchitectureRules(t *testing.T) {
	for _, p := range check(os.DirFS("../..")) {
		t.Error(p)
	}
}

func src(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte("package x\n" + s)} }

func TestCheckCatchesViolations(t *testing.T) {
	const mod = `"github.com/samarthsinh2660/safeshell/internal/`
	tests := []struct {
		name, file, src, want string
	}{
		{"pure uses os", "internal/policy/a.go", `import "os"`, `internal/policy/a.go imports "os": internal/policy must not use`},
		{"pure uses exec", "internal/domain/a.go", `import "os/exec"`, `"os/exec"`},
		{"classify rules use net", "internal/classify/rules/a.go", `import "net/http"`, `"net/http"`},
		{"pure imports adapter", "internal/report/a.go", `import _ ` + mod + `snapshot"`, "may import only other pure packages (internal/snapshot is not one)"},
		{"pure imports third party", "internal/policy/a.go", `import _ "golang.org/x/sys/unix"`, "only the standard library and the shell parser library"},
		{"pure reads the clock", "internal/classify/a.go", "import \"time\"\nvar _ = time.Now()", "uses time.Now"},
		{"engine imports adapter", "internal/engine/a.go", `import _ ` + mod + `audit"`, "may import only pure packages"},
		{"engine uses os", "internal/engine/a.go", `import "os"`, `internal/engine must not use`},
		{"adapter imports engine", "internal/audit/a.go", `import _ ` + mod + `engine"`, "only cmd/safeshell may import the engine"},
		{"eval imports engine", "eval/a.go", `import _ ` + mod + `engine"`, "only cmd/safeshell may import the engine"},
		{"broken file", "internal/audit/a.go", `this is not Go`, "cannot be read or parsed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := check(fstest.MapFS{tc.file: src(tc.src)})
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Fatalf("want one problem containing %q, got %q", tc.want, got)
			}
		})
	}
}

func TestCheckAllowsTheDesignedImports(t *testing.T) {
	const mod = `"github.com/samarthsinh2660/safeshell/internal/`
	fsys := fstest.MapFS{
		"internal/report/a.go":     src(`import ("fmt"; "path/filepath"; "time"; _ ` + mod + `sandbox")`),
		"internal/shellparse/a.go": src(`import _ "mvdan.cc/sh/v3/syntax"`),
		"internal/engine/a.go":     src(`import ("context"; _ ` + mod + `policy")`),
		"cmd/safeshell/main.go":    src(`import ("os"; _ ` + mod + `engine"; _ ` + mod + `audit")`),
		"internal/audit/a.go":      src(`import "os"`),
		"internal/audit/a_test.go": src(`import _ ` + mod + `engine"`),
	}
	if got := check(fsys); len(got) != 0 {
		t.Fatalf("unexpected problems: %q", got)
	}
}
