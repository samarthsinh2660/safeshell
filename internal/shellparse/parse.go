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
	// AliasWord marks the command an alias gives as its whole value, such as
	// `alias k=kubectl`. It is only another name, so it is rated only when
	// the word alone does harm.
	AliasWord bool
	// Bare marks a plain NAME=value line. It is a command named export for the
	// rules, but is shown as typed.
	Bare bool
	// Substituted are the words of the commands inside $(...) and <(...) in
	// the arguments, so a file read there can still be seen.
	Substituted []string
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
	if c.Bare {
		parts = slices.Clone(c.Args)
	}
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
	// ChangesShell is true when the line sets up the running shell itself:
	// it defines a function, sets a variable, or runs a builtin like export, alias or source.
	ChangesShell bool
}

// shellBuiltins are commands whose effect lasts in the shell that runs them.
var shellBuiltins = map[string]bool{
	"alias": true, "unalias": true, "export": true, "declare": true, "typeset": true, "readonly": true,
	"local": true, "unset": true, "trap": true, "eval": true, "source": true, ".": true, "set": true,
	"shopt": true, "bind": true, "enable": true, "builtin": true, "hash": true,
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
	// aliasWord is set while reading an alias whose value is one bare word.
	aliasWord bool
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
	if _, ok := s.Cmd.(*syntax.DeclClause); ok { // export, declare, typeset, readonly, local
		w.res.ChangesShell = true
	}
	switch c := s.Cmd.(type) {
	case nil:
		if len(s.Redirs) > 0 {
			w.single(Command{Redirs: redirects(s.Redirs), Background: s.Background, Remote: w.remote})
		}
	case *syntax.CallExpr:
		if len(c.Args) == 0 && len(c.Assigns) > 0 || len(c.Args) > 0 && shellBuiltins[wordString(c.Args[0])] {
			w.res.ChangesShell = true
		}
		if cmd, ok := w.build(c, s, depth); ok {
			w.single(cmd)
			if cmd.Name == "cd" {
				w.cd(cmd.Args)
			}
		} else if len(c.Args) == 0 && len(c.Assigns) > 0 {
			w.assign("export", true, c.Assigns, s, depth)
		}
	case *syntax.DeclClause:
		w.assign(c.Variant.Value, false, c.Args, s, depth)
		saved := w.cwd
		w.children(c, depth)
		w.cwd = saved
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			w.pipeline(s, depth)
			return
		}
		w.stmt(c.X, depth)
		w.stmt(c.Y, depth)
	case *syntax.FuncDecl:
		w.res.ChangesShell = true
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
	var carried string // lines echoed by an earlier ( ... ) or { ...; } stage
	for _, st := range stmts {
		if call, ok := st.Cmd.(*syntax.CallExpr); ok {
			cmd, ok := w.build(call, st, depth)
			if !ok {
				continue
			}
			cmd.Background = s.Background
			if n := len(p.Cmds); n > 0 {
				carried += pipedText(p.Cmds[n-1])
			}
			if carried != "" {
				cmd.Stdin += carried
				w.stdinCode(&cmd, depth)
				w.startupText(&cmd, depth)
			}
			carried = ""
			p.Cmds = append(p.Cmds, cmd)
			continue
		}
		carried += echoed(st)
		w.stmt(st, depth)
	}
	if len(p.Cmds) > 0 {
		w.res.Pipelines = append(w.res.Pipelines, p)
	}
}

// pipedText is the text that prev, an earlier stage of a pipeline, passes on
// when it is only echo, printf, or cat of a here-document or here-string.
func pipedText(prev Command) string {
	switch prev.Name {
	case "echo", "printf":
		// printf (and echo -e, or dash's echo) turn \n into a new line before the shell reads it.
		return strings.ReplaceAll(echoText(prev.Name, prev.Args), `\n`, "\n")
	case "cat":
		if prev.Stdin != "" && !slices.ContainsFunc(prev.Args, func(a string) bool { return !strings.HasPrefix(a, "-") }) {
			return "\n" + prev.Stdin
		}
	}
	return ""
}

// build converts one CallExpr into a Command and queues any code hidden inside
// it (command substitutions, `sh -c`, ssh, find -exec) for analysis.
func (w *walker) build(call *syntax.CallExpr, st *syntax.Stmt, depth int) (Command, bool) {
	words := make([]string, 0, len(call.Args))
	downloads := false
	var fetched []int // indexes in words
	var substituted []string
	walk := func(a syntax.Node, idx int) { // idx is the argument holding a, or -1 for an assignment
		syntax.Walk(a, func(n syntax.Node) bool {
			switch x := n.(type) {
			case *syntax.CmdSubst:
				if idx >= 0 {
					substituted = append(substituted, substWords(x)...)
				}
				saved := w.cwd
				for _, s := range x.Stmts {
					if idx >= 0 && fetchesNetwork(s) && !slices.Contains(fetched, idx) {
						fetched = append(fetched, idx)
					}
					w.stmt(s, depth)
				}
				w.cwd = saved
				return false
			case *syntax.ProcSubst:
				if idx >= 0 {
					substituted = append(substituted, substWords(x)...)
				}
				saved := w.cwd
				for _, s := range x.Stmts {
					if idx >= 0 && fetchesNetwork(s) {
						downloads = true
					}
					w.stmt(s, depth)
				}
				w.cwd = saved
				return false
			}
			return true
		})
	}
	for _, a := range call.Assigns {
		walk(a, -1)
	}
	for _, a := range call.Args {
		words = append(words, wordString(a))
		walk(a, len(words)-1)
	}
	if len(words) == 0 {
		return Command{}, false
	}
	cmd := Command{Background: st.Background, Remote: w.remote, Downloads: downloads, Redirs: redirects(st.Redirs), Cwd: w.cwd, Stdin: hereText(st.Redirs), Substituted: substituted, AliasWord: w.aliasWord}
	rest := unwrap(words, &cmd)
	if len(rest) == 0 {
		// `exec > file` has no command left, but the redirect still happens.
		return cmd, len(cmd.Redirs) > 0
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
		w.startupText(&cmd, depth)
	}
	return cmd, true
}

// substWords returns every word written inside a substitution.
func substWords(n syntax.Node) []string {
	var ws []string
	syntax.Walk(n, func(n syntax.Node) bool {
		if w, ok := n.(*syntax.Word); ok {
			ws = append(ws, wordString(w))
		}
		return true
	})
	return ws
}

// assign records an assignment that changes the running shell, such as
// `export PATH=...` or a bare `PROMPT_COMMAND=...`, as a command named name with
// NAME=value arguments. PROMPT_COMMAND runs its value before every prompt, so
// that text is queued as code.
func (w *walker) assign(name string, bare bool, as []*syntax.Assign, s *syntax.Stmt, depth int) {
	cmd := Command{Name: name, Redirs: redirects(s.Redirs), Remote: w.remote, Cwd: w.cwd, Bare: bare}
	for _, a := range as {
		var arg string
		switch {
		case a.Name == nil:
			arg = wordString(a.Value)
		case a.Naked:
			arg = a.Name.Value
		default:
			op := "="
			if a.Append {
				op = "+="
			}
			arg = a.Name.Value + op
			if a.Value != nil {
				val := wordString(a.Value)
				arg += val
				if a.Name.Value == "PROMPT_COMMAND" {
					w.deferred(strings.TrimLeft(val, "; \t\n"), depth)
				}
			}
			if a.Array != nil && a.Name.Value == "PROMPT_COMMAND" { // PROMPT_COMMAND=('cmd' ...)
				for _, el := range a.Array.Elems {
					if el.Value != nil {
						w.deferred(strings.TrimLeft(wordString(el.Value), "; \t\n"), depth)
					}
				}
			}
		}
		cmd.Args = append(cmd.Args, arg)
	}
	w.single(cmd)
}

// nested queues code embedded in the arguments of c.
func (w *walker) nested(c *Command, depth int) {
	switch c.Name {
	case "alias": // the value runs whenever the alias is used
		for _, a := range c.Args {
			if _, val, ok := strings.Cut(a, "="); ok {
				w.aliasWord = len(strings.Fields(val)) == 1
				w.deferred(val, depth)
				w.aliasWord = false
			}
		}
	case "trap": // the action runs when the signal or EXIT arrives
		args := c.Args
		if len(args) > 0 && args[0] == "--" {
			args = args[1:]
		}
		if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
			w.deferred(args[0], depth)
		}
	case "sh", "bash", "zsh", "dash", "ksh":
		if code, ok := shellCode(c.Args); ok {
			_ = w.parseString(code, depth+1)
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
		w.findExec(c, depth)
	case "chroot": // chroot itself stays rated; the command inside is rated too
		if rest := chrootCommand(c.Args); len(rest) > 0 {
			w.inner(c, rest, false, depth)
		}
	case "sed":
		execs, _ := SedActions(c.Args)
		for _, sh := range execs {
			_ = w.parseString(sh, depth+1)
		}
	case "python", "python3", "perl", "ruby", "node":
		for _, sh := range embeddedShell(InlineCode(*c)) {
			_ = w.parseString(sh, depth+1)
		}
	}
}

// shellCode returns the code a shell runs for `-c`: the first operand after
// its options. Options may sit before or after the -c cluster, and -o / -O
// take a value of their own (bash -Oc extglob CODE).
func shellCode(args []string) (string, bool) {
	hasC := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			i++
			return operand(args, i, hasC)
		case strings.HasPrefix(a, "--"):
		case strings.HasPrefix(a, "-") && len(a) > 1:
			hasC = hasC || strings.Contains(a, "c")
			i += strings.Count(a, "o") + strings.Count(a, "O")
		default:
			return operand(args, i, hasC)
		}
	}
	return "", false
}

// operand returns args[i] when the shell was given -c.
func operand(args []string, i int, hasC bool) (string, bool) {
	if hasC && i < len(args) {
		return args[i], true
	}
	return "", false
}

// isCFlag reports whether a is `-c` or a short-option cluster holding it,
// such as -lc, -xc or -ec.
func isCFlag(a string) bool {
	return strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c")
}

// stdinCode analyses text a shell reads from stdin, as in `echo "rm -rf /" | sh`.
func (w *walker) stdinCode(c *Command, depth int) {
	if strings.TrimSpace(c.Stdin) == "" || depth >= maxDepth {
		return
	}
	switch c.Name {
	case "source", ".":
		if len(c.Args) > 0 && c.Args[0] == "/dev/stdin" {
			_ = w.parseString(c.Stdin, depth+1)
		}
	case "sh", "bash", "zsh", "dash", "ksh":
		for _, a := range c.Args {
			if a == "-s" || a == "--" {
				break // what follows are arguments for the script read from stdin
			}
			if !strings.HasPrefix(a, "-") {
				return // runs a named script, not stdin
			}
			if isCFlag(a) {
				return
			}
		}
		_ = w.parseString(c.Stdin, depth+1)
	case "crontab": // each job line is a command that runs later
		for i := 0; i < len(c.Args); i++ {
			if c.Args[i] == "-u" {
				i++ // the user name
			} else if !strings.HasPrefix(c.Args[i], "-") {
				return // installs a named file, not stdin
			}
		}
		for _, line := range strings.Split(strings.ReplaceAll(c.Stdin, `\n`, "\n"), "\n") {
			w.deferred(cronCommand(line), depth)
		}
	}
}

// cronCommand returns the command of one crontab line: the text after the five
// time fields (or after @reboot, @daily, ...). Comments and variable lines give "".
func cronCommand(line string) string {
	fields := strings.Fields(line)
	skip := 5
	if len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		skip = 1
	}
	if len(fields) <= skip || strings.HasPrefix(fields[0], "#") {
		return ""
	}
	return strings.Join(fields[skip:], " ")
}

// cd updates the tracked working directory after `cd args`.
func (w *walker) cd(args []string) { w.cwd = chdir(w.cwd, args) }

// chdir is the directory after `cd args` run from cwd.
func chdir(cwd string, args []string) string {
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
		return "~"
	case n > 1 || dir == "-" || strings.ContainsAny(dir, "$`?"):
		return "?"
	case strings.HasPrefix(dir, "/") || strings.HasPrefix(dir, "~"):
		return path.Clean(dir)
	case cwd == "?":
		return cwd
	}
	return path.Join(cwd, dir)
}

func (w *walker) findExec(c *Command, depth int) {
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if a != "-exec" && a != "-execdir" && a != "-ok" && a != "-okdir" {
			continue
		}
		end := i + 1
		for end < len(c.Args) && c.Args[end] != ";" && c.Args[end] != "+" {
			end++
		}
		w.inner(c, c.Args[i+1:end], true, depth)
		i = end // inner -exec words belong to the inner command, not to this find
	}
}

// inner records the command that words spell out, run by c (find -exec, chroot).
func (w *walker) inner(c *Command, words []string, viaFind bool, depth int) {
	sub := Command{Remote: c.Remote, ViaFind: viaFind}
	rest := unwrap(slices.Clone(words), &sub)
	if len(rest) > 0 {
		sub.Name, sub.Args = rest[0], rest[1:]
		w.single(sub)
		if depth+1 < maxDepth {
			w.nested(&sub, depth+1)
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
			args = skipOpts(args[1:], "-u", "-g", "-p", "-C", "-r", "-t", "-U", "-D", "-T", "-R", "-c", "-a",
				"--user", "--group", "--host", "--prompt", "--close-from", "--role", "--type", "--other-user", "--chdir",
				"--chroot", "--command-timeout", "--login-class")
		case "env":
			whole := args
			args = args[1:]
			for len(args) > 0 && (strings.Contains(args[0], "=") || strings.HasPrefix(args[0], "-")) {
				if src, n := envSplit(args); n > 0 {
					words := splitWords(strings.ReplaceAll(src, `\_`, " "))
					if words == nil {
						return whole // not one plain command: leave env as the command, do not hide the rest
					}
					args = append(words, args[n:]...)
					continue
				}
				if takesValue(args[0], "-C", "--chdir") && len(args) > 1 {
					cmd.Cwd = chdir(cmd.Cwd, args[1:2])
				}
				if strings.HasPrefix(args[0], "--chdir=") {
					cmd.Cwd = chdir(cmd.Cwd, []string{strings.TrimPrefix(args[0], "--chdir=")})
				}
				if takesValue(args[0], "-u", "-C", "--unset", "--chdir") {
					args = args[1:]
				}
				if len(args) > 0 {
					args = args[1:]
				}
			}
		case "su", "runuser", "pkexec", "nsenter", "watch", "flock", "strace", "parallel":
			rest := wrapped(args, cmd)
			if len(rest) == 0 {
				return args // nothing to run: the wrapper itself is the command
			}
			args = rest
		case "nohup", "time", "command", "builtin", "setsid", "busybox":
			args = skipOpts(args[1:])
		case "exec":
			args = skipOpts(args[1:], "-a") // -a NAME only renames argv[0]
		case "stdbuf":
			args = skipOpts(args[1:], "-i", "-o", "-e", "--input", "--output", "--error")
		case "nice", "ionice":
			args = skipOpts(args[1:], "-n", "-c", "--adjustment", "--class", "--classdata")
		case "timeout":
			args = skipOpts(args[1:], "-s", "-k", "--signal", "--kill-after")
			if len(args) > 0 { // the DURATION operand
				args = args[1:]
			}
		case "xargs":
			cmd.ViaXargs = true
			args = skipOpts(args[1:], "-n", "-I", "-P", "-L", "-d", "-s", "-a", "-E",
				"--max-args", "--max-procs", "--max-lines", "--max-chars", "--delimiter", "--arg-file")
		default:
			return args
		}
	}
	return args
}

// systemBins are where the standard tools live, so /bin/rm is just rm.
var systemBins = []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin", "/usr/local/bin", "/usr/local/sbin"}

// programName undoes the way of writing one program that would hide it from
// the rules: the full path of a system tool (/bin/rm). Other paths, such as
// ./rm, are left alone. (Backslashes such as \rm are already gone from words.)
func programName(word string) string {
	if slices.Contains(systemBins, path.Dir(word)) {
		return path.Base(word)
	}
	return word
}

// skipOpts drops leading options; those named in withValue also consume the next word.
func skipOpts(args []string, withValue ...string) []string {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") && args[0] != "-" {
		takes := takesValue(args[0], withValue...)
		args = args[1:]
		if takes && len(args) > 0 {
			args = args[1:]
		}
	}
	return args
}

// takesValue reports whether option opt takes the next word as its value. A
// short-option cluster counts when the letter that takes a value comes last
// (-iu NAME); `-uNAME` carries its value inside the word.
func takesValue(opt string, withValue ...string) bool {
	if strings.HasPrefix(opt, "--") {
		return slices.Contains(withValue, opt)
	}
	for i := 1; i < len(opt); i++ {
		if slices.Contains(withValue, "-"+opt[i:i+1]) {
			return i == len(opt)-1
		}
	}
	return false
}

// envSplit finds the string given to `env -S` (also --split-string) at the
// front of args and returns it with the number of words it took, or 0 if none.
func envSplit(args []string) (string, int) {
	a := args[0]
	switch {
	case takesValue(a, "-S", "--split-string"):
		if len(args) > 1 {
			return args[1], 2
		}
	case strings.HasPrefix(a, "--split-string="):
		return strings.TrimPrefix(a, "--split-string="), 1
	case strings.HasPrefix(a, "-S") && len(a) > 2:
		return a[2:], 1
	}
	return "", 0
}

// splitWords breaks a string into shell words; it is nil if the text is not valid shell.
func splitWords(src string) []string {
	f, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(src), "")
	if err != nil || len(f.Stmts) == 0 {
		return nil
	}
	call, ok := f.Stmts[0].Cmd.(*syntax.CallExpr)
	if !ok {
		return nil
	}
	words := make([]string, 0, len(call.Args))
	for _, a := range call.Args {
		words = append(words, wordString(a))
	}
	return words
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

// wordString flattens a shell word the way the shell reads it: quotes and
// backslash escapes are removed, so r\m, 'r'm and \/ are rm and /. Variables
// are kept as $NAME and command substitutions as $(...), because their values
// are unknown until run time.
func wordString(w *syntax.Word) string { return flatten(w, true) }

// rawString is wordString with backslashes left in, for here-document text
// that a shell reads again later.
func rawString(w *syntax.Word) string { return flatten(w, false) }

func flatten(w *syntax.Word, unescape bool) string {
	var sb strings.Builder
	var parts func([]syntax.WordPart, bool)
	parts = func(ps []syntax.WordPart, quoted bool) {
		for _, p := range ps {
			switch x := p.(type) {
			case *syntax.Lit:
				if unescape {
					sb.WriteString(unescapeLit(x.Value, quoted))
				} else {
					sb.WriteString(x.Value)
				}
			case *syntax.SglQuoted:
				sb.WriteString(x.Value)
			case *syntax.DblQuoted:
				parts(x.Parts, true)
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
	parts(w.Parts, false)
	return sb.String()
}

// unescapeLit removes the backslashes the shell removes: before any character
// outside quotes, and only before $ ` " \ and a newline inside double quotes.
func unescapeLit(s string, inDouble bool) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			next := s[i+1]
			if !inDouble || strings.IndexByte("$`\"\\", next) >= 0 {
				i++
			}
		}
		sb.WriteByte(s[i])
	}
	return sb.String()
}

// hereText returns the text of here-strings and here-documents among rs.
func hereText(rs []*syntax.Redirect) string {
	var parts []string
	for _, r := range rs {
		switch {
		case r.Hdoc != nil:
			parts = append(parts, rawString(r.Hdoc))
		case r.Op == syntax.WordHdoc && r.Word != nil:
			parts = append(parts, wordString(r.Word))
		}
	}
	return strings.Join(parts, "\n")
}
