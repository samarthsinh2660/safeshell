// Package shellparse turns a shell command line into a flat, analysable form.
//
// It understands pipelines, && / || lists, subshells, $(...) and <(...),
// wrappers such as sudo / env / xargs, and nested code in `sh -c`, `eval`,
// `ssh host '...'` and `find -exec`. The risk classifier never sees raw text.
package shellparse

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const maxDepth = 5

// Redirect is one output/input redirection such as `> file` or `>> log`.
type Redirect struct {
	Op     string
	Target string
}

// Command is a single simple command after wrappers have been stripped.
type Command struct {
	Name       string
	Args       []string
	Sudo       bool
	ViaXargs   bool // operands come from stdin, so targets are unknown
	ViaFind    bool // run by `find -exec`, targets are unknown
	Background bool
	Remote     string // non-empty when it runs on another host over ssh
	Downloads  bool   // an argument is a <(...) that fetches from the network
	// FetchedArgs are the indexes in Args of words holding a $(...) that
	// downloads, as in  eval "$(curl ...)"  or  sh -c "$(curl ...)".
	FetchedArgs []int
	Redirs      []Redirect
	// Stdin is text fed to the command by a pipe from echo/printf, a here-string
	// or a here-document. It is how `echo "DROP TABLE x" | psql` is seen.
	Stdin string
	// Cwd is where the command runs, tracked through `cd` on the same line.
	// "" means the directory the line started in; "?" means unknown.
	Cwd string
}

// String renders the command roughly as typed, for use in reports.
func (c Command) String() string {
	parts := append([]string{c.Name}, c.Args...)
	if c.Sudo {
		parts = append([]string{"sudo"}, parts...)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

// Pipeline is a group of commands connected by `|`. A lone command is a
// pipeline of length one.
type Pipeline struct{ Cmds []Command }

// Result is everything found in one command line.
type Result struct {
	Pipelines []Pipeline
	ForkBomb  bool
}

// Parse analyses line. It returns an error only if the line is not valid shell.
func Parse(line string) (*Result, error) {
	w := &walker{res: &Result{}}
	if err := w.parseString(line, 0); err != nil {
		return nil, err
	}
	return w.res, nil
}

type walker struct {
	res    *Result
	remote string
	cwd    string
}

func (w *walker) parseString(src string, depth int) error {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil {
		return err
	}
	for _, st := range f.Stmts {
		w.stmt(st, depth)
	}
	return nil
}

func (w *walker) single(c Command) {
	w.res.Pipelines = append(w.res.Pipelines, Pipeline{Cmds: []Command{c}})
}

func (w *walker) stmt(s *syntax.Stmt, depth int) {
	switch c := s.Cmd.(type) {
	case nil:
		if len(s.Redirs) > 0 {
			w.single(Command{Redirs: redirects(s.Redirs), Background: s.Background, Remote: w.remote})
		}
	case *syntax.CallExpr:
		if cmd, ok := w.build(c, s, depth); ok {
			w.single(cmd)
			if cmd.Name == "cd" {
				w.cd(cmd.Args)
			}
		}
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.pipeline(s, depth)
			return
		}
		w.stmt(c.X, depth)
		w.stmt(c.Y, depth)
	case *syntax.FuncDecl:
		if isForkBomb(c) {
			w.res.ForkBomb = true
		}
		if c.Body != nil {
			w.stmt(c.Body, depth)
		}
	case *syntax.Subshell:
		saved := w.cwd
		w.children(c, depth)
		w.cwd = saved // a subshell's cd does not leak out
		if len(s.Redirs) > 0 {
			w.single(Command{Redirs: redirects(s.Redirs), Background: s.Background, Remote: w.remote})
		}
	default:
		before := w.cwd
		w.children(c, depth)
		if _, isBlock := c.(*syntax.Block); !isBlock && w.cwd != before {
			w.cwd = "?" // a cd inside if/for/while may or may not have run
		}
		if len(s.Redirs) > 0 {
			w.single(Command{Redirs: redirects(s.Redirs), Background: s.Background, Remote: w.remote})
		}
	}
}

// children visits the statements nested inside a compound command
// (if/for/while/case/subshell/block) without descending into them twice.
func (w *walker) children(root syntax.Node, depth int) {
	syntax.Walk(root, func(n syntax.Node) bool {
		if n == nil || n == root {
			return n != nil
		}
		if st, ok := n.(*syntax.Stmt); ok {
			w.stmt(st, depth)
			return false
		}
		return true
	})
}

func (w *walker) pipeline(s *syntax.Stmt, depth int) {
	var stmts []*syntax.Stmt
	var flat func(*syntax.Stmt)
	flat = func(st *syntax.Stmt) {
		if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && (b.Op == syntax.Pipe || b.Op == syntax.PipeAll) {
			flat(b.X)
			flat(b.Y)
			return
		}
		stmts = append(stmts, st)
	}
	flat(s)
	var p Pipeline
	for _, st := range stmts {
		if call, ok := st.Cmd.(*syntax.CallExpr); ok {
			cmd, ok := w.build(call, st, depth)
			if !ok {
				continue
			}
			cmd.Background = s.Background
			if n := len(p.Cmds); n > 0 && (p.Cmds[n-1].Name == "echo" || p.Cmds[n-1].Name == "printf") {
				cmd.Stdin += " " + strings.Join(p.Cmds[n-1].Args, " ")
				w.stdinCode(&cmd, depth)
			}
			p.Cmds = append(p.Cmds, cmd)
			continue
		}
		w.stmt(st, depth)
	}
	if len(p.Cmds) > 0 {
		w.res.Pipelines = append(w.res.Pipelines, p)
	}
}

// build converts one CallExpr into a Command and queues any code hidden inside
// it (command substitutions, `sh -c`, ssh, find -exec) for analysis.
func (w *walker) build(call *syntax.CallExpr, st *syntax.Stmt, depth int) (Command, bool) {
	words := make([]string, 0, len(call.Args))
	downloads := false
	var fetched []int // indexes in words
	for _, a := range call.Args {
		words = append(words, wordString(a))
		idx := len(words) - 1
		syntax.Walk(a, func(n syntax.Node) bool {
			switch x := n.(type) {
			case *syntax.CmdSubst:
				saved := w.cwd
				for _, s := range x.Stmts {
					if fetchesNetwork(s) && !slices.Contains(fetched, idx) {
						fetched = append(fetched, idx)
					}
					w.stmt(s, depth)
				}
				w.cwd = saved
				return false
			case *syntax.ProcSubst:
				for _, s := range x.Stmts {
					if fetchesNetwork(s) {
						downloads = true
					}
					w.stmt(s, depth)
				}
				return false
			}
			return true
		})
	}
	if len(words) == 0 {
		return Command{}, false
	}
	cmd := Command{Background: st.Background, Remote: w.remote, Downloads: downloads, Redirs: redirects(st.Redirs), Cwd: w.cwd, Stdin: hereText(st.Redirs)}
	rest := unwrap(words, &cmd)
	if len(rest) == 0 {
		return cmd, false
	}
	cmd.Name, cmd.Args = rest[0], rest[1:]
	for _, i := range fetched {
		if j := i - (len(words) - len(rest)) - 1; j >= 0 {
			cmd.FetchedArgs = append(cmd.FetchedArgs, j)
		}
	}
	if depth < maxDepth {
		w.nested(&cmd, depth)
		w.stdinCode(&cmd, depth)
	}
	return cmd, true
}

// nested queues code embedded in the arguments of c.
func (w *walker) nested(c *Command, depth int) {
	switch c.Name {
	case "sh", "bash", "zsh", "dash", "ksh":
		for i, a := range c.Args {
			if a == "-c" && i+1 < len(c.Args) {
				_ = w.parseString(c.Args[i+1], depth+1)
				return
			}
		}
	case "eval":
		_ = w.parseString(strings.Join(c.Args, " "), depth+1)
	case "ssh":
		host, remote := sshSplit(c.Args)
		if remote != "" {
			prev := w.remote
			w.remote = host
			_ = w.parseString(remote, depth+1)
			w.remote = prev
		}
	case "find":
		w.findExec(c)
	case "python", "python3", "perl", "ruby", "node":
		for _, sh := range embeddedShell(InlineCode(*c)) {
			_ = w.parseString(sh, depth+1)
		}
	}
}

// stdinCode analyses text a shell reads from stdin, as in `echo "rm -rf /" | sh`.
func (w *walker) stdinCode(c *Command, depth int) {
	if strings.TrimSpace(c.Stdin) == "" || depth >= maxDepth {
		return
	}
	switch c.Name {
	case "sh", "bash", "zsh", "dash", "ksh":
		for _, a := range c.Args {
			if !strings.HasPrefix(a, "-") {
				return // runs a named script, not stdin
			}
			if a == "-c" {
				return
			}
		}
		_ = w.parseString(c.Stdin, depth+1)
	}
}

// cd updates the tracked working directory after `cd args`.
func (w *walker) cd(args []string) {
	var dir string
	n := 0
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			continue
		}
		dir = a
		n++
	}
	switch {
	case n == 0:
		w.cwd = "~"
	case n > 1 || dir == "-" || strings.ContainsAny(dir, "$`?"):
		w.cwd = "?"
	case strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "~"):
		w.cwd = path.Clean(dir)
	case w.cwd == "?":
	default:
		w.cwd = path.Join(w.cwd, dir)
	}
}

func (w *walker) findExec(c *Command) {
	for i, a := range c.Args {
		if a != "-exec" && a != "-execdir" && a != "-ok" && a != "-okdir" {
			continue
		}
		var inner []string
		for _, b := range c.Args[i+1:] {
			if b == ";" || b == "+" || b == `\;` {
				break
			}
			inner = append(inner, b)
		}
		sub := Command{Remote: c.Remote, ViaFind: true}
		inner = unwrap(inner, &sub)
		if len(inner) > 0 {
			sub.Name, sub.Args = inner[0], inner[1:]
			w.single(sub)
		}
	}
}

// sshSplit returns the host and the remote command string of an ssh invocation.
func sshSplit(args []string) (host, remote string) {
	withValue := map[string]bool{"-p": true, "-i": true, "-l": true, "-o": true, "-F": true, "-J": true, "-L": true, "-R": true, "-D": true, "-b": true, "-c": true, "-m": true, "-E": true, "-e": true, "-S": true, "-W": true}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case withValue[a]:
			i++
		case strings.HasPrefix(a, "-"):
		default:
			return a, strings.Join(args[i+1:], " ")
		}
	}
	return "", ""
}

// unwrap strips sudo/env/nohup/time/xargs/... from the front of args, recording
// what it saw on cmd, and returns the real command and its arguments.
func unwrap(args []string, cmd *Command) []string {
	for len(args) > 0 {
		args[0] = programName(args[0])
		switch args[0] {
		case "sudo", "doas":
			cmd.Sudo = true
			args = skipOpts(args[1:], "-u", "-g", "-h", "-p", "-C", "-r", "-t", "-U", "-D")
		case "env":
			args = args[1:]
			for len(args) > 0 && (strings.Contains(args[0], "=") || strings.HasPrefix(args[0], "-")) {
				if args[0] == "-u" || args[0] == "-C" {
					args = args[1:]
				}
				if len(args) > 0 {
					args = args[1:]
				}
			}
		case "nohup", "time", "command", "builtin", "exec", "setsid", "stdbuf", "busybox":
			args = skipOpts(args[1:])
		case "nice", "ionice":
			args = skipOpts(args[1:], "-n", "-c")
		case "timeout":
			args = skipOpts(args[1:], "-s", "-k")
			if len(args) > 0 { // the DURATION operand
				args = args[1:]
			}
		case "xargs":
			cmd.ViaXargs = true
			args = skipOpts(args[1:], "-n", "-I", "-P", "-L", "-d", "-s", "-a", "-E", "-l", "-i")
		default:
			return args
		}
	}
	return args
}

// systemBins are where the standard tools live, so /bin/rm is just rm.
var systemBins = []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin"}

// programName undoes the ways of writing one program that would hide it from
// the rules: a leading backslash (\rm, the usual way to skip an alias) and the
// full path of a system tool (/bin/rm). Other paths, such as ./rm, are left alone.
func programName(word string) string {
	word = strings.TrimLeft(word, `\`)
	if slices.Contains(systemBins, path.Dir(word)) {
		return path.Base(word)
	}
	return word
}

// skipOpts drops leading options; those named in withValue also consume the next word.
func skipOpts(args []string, withValue ...string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "-" {
		takes := false
		for _, v := range withValue {
			if args[0] == v {
				takes = true
			}
		}
		args = args[1:]
		if takes && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

func fetchesNetwork(s *syntax.Stmt) bool {
	found := false
	syntax.Walk(s, func(n syntax.Node) bool {
		if c, ok := n.(*syntax.CallExpr); ok && len(c.Args) > 0 {
			if name := programName(wordString(c.Args[0])); name == "curl" || name == "wget" {
				found = true
			}
		}
		return !found
	})
	return found
}

func isForkBomb(fd *syntax.FuncDecl) bool {
	if fd.Body == nil || fd.Name == nil {
		return false
	}
	name := fd.Name.Value
	called := func(s *syntax.Stmt) bool {
		c, ok := s.Cmd.(*syntax.CallExpr)
		return ok && len(c.Args) > 0 && wordString(c.Args[0]) == name
	}
	bomb := false
	syntax.Walk(fd.Body, func(n syntax.Node) bool {
		if b, ok := n.(*syntax.BinaryCmd); ok && b.Op == syntax.Pipe && called(b.X) && called(b.Y) {
			bomb = true
		}
		return !bomb
	})
	return bomb
}

func redirects(rs []*syntax.Redirect) []Redirect {
	var out []Redirect
	for _, r := range rs {
		if r.Word == nil {
			continue
		}
		out = append(out, Redirect{Op: r.Op.String(), Target: wordString(r.Word)})
	}
	return out
}

// wordString flattens a shell word. Variables are kept as $NAME and command
// substitutions as $(...), because their values are unknown until run time.
func wordString(w *syntax.Word) string {
	var sb strings.Builder
	var parts func([]syntax.WordPart)
	parts = func(ps []syntax.WordPart) {
		for _, p := range ps {
			switch x := p.(type) {
			case *syntax.Lit:
				sb.WriteString(x.Value)
			case *syntax.SglQuoted:
				sb.WriteString(x.Value)
			case *syntax.DblQuoted:
				parts(x.Parts)
			case *syntax.ParamExp:
				if x.Param != nil {
					fmt.Fprintf(&sb, "$%s", x.Param.Value)
				}
			case *syntax.CmdSubst:
				sb.WriteString("$(...)")
			default:
				sb.WriteString("?")
			}
		}
	}
	parts(w.Parts)
	return sb.String()
}

// hereText returns the text of here-strings and here-documents among rs.
func hereText(rs []*syntax.Redirect) string {
	var parts []string
	for _, r := range rs {
		switch {
		case r.Hdoc != nil:
			parts = append(parts, wordString(r.Hdoc))
		case r.Op == syntax.WordHdoc && r.Word != nil:
			parts = append(parts, wordString(r.Word))
		}
	}
	return strings.Join(parts, "\n")
}
