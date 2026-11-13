package effects

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// GitRunner runs one read-only git command in dir and returns its output.
// Production code runs real git with a short timeout and no prompts; tests
// supply canned output or a real temporary repository.
type GitRunner func(dir string, args ...string) (string, error)

var (
	errGitNotAllowed = errors.New("refused to run a git command that is not on the read-only list")
	errGitNoRemote   = errors.New("no remote branch to compare with")
	errGitNoTarget   = errors.New("the target is not a commit that can be looked up here")
	errGitPrograms   = errors.New("the repository's settings run programs")
)

// gitSafeArgs go in front of every query. They switch off the settings that
// would make a read-only command run a program from the repository: the file
// system monitor, signature checks (they run gpg.program), hooks (git runs
// post-index-change when it refreshes the index), the pager, and any network
// access (a partial clone would fetch a missing object through core.sshCommand).
var gitSafeArgs = []string{"-c", "core.fsmonitor=false", "-c", "log.showSignature=false", "-c", "core.pager=cat",
	"-c", "core.hooksPath=/dev/null", "-c", "protocol.allow=never", "--no-pager"}

// gitQuery runs only git commands on a short allow-list of read-only ones, so
// a mistake in this file can never change a repository.
type gitQuery struct {
	run GitRunner
	dir string
	cfg gitConfig
}

func (g gitQuery) out(args ...string) (string, error) {
	ok := false
	switch args[0] {
	case "rev-parse":
		ok = true
	case "status": // submodules have settings of their own that are never checked, so they are not entered
		ok = true
		args = append([]string{"status", "--ignore-submodules=all"}, args[1:]...)
	case "log": // never run an external diff program or a text conversion filter
		ok = true
		args = append([]string{"log", "--no-ext-diff", "--no-textconv"}, args[1:]...)
	case "stash":
		ok = len(args) > 1 && args[1] == "list"
	case "clean":
		ok = len(args) > 1 && args[1] == "-n"
	case "config": // only lists settings, which runs nothing; the safety options would hide a monitor set from the environment
		if len(args) > 1 && args[1] == "--list" {
			return g.run(g.dir, args...)
		}
	}
	if !ok {
		return "", errGitNotAllowed
	}
	return g.run(g.dir, append(append([]string{}, gitSafeArgs...), args...)...)
}

// runsPrograms is true for settings that make git start another program even
// for a read-only command, and that -c cannot switch off: filters and text
// converters and an external diff. (The file system monitor, signature programs
// and hooks are overridden on every query instead.)
func runsPrograms(key string) bool {
	return strings.HasPrefix(key, "filter.") || key == "diff.external" || (strings.HasPrefix(key, "diff.") && strings.HasSuffix(key, ".textconv"))
}

// gitConfig is the settings of a repository that the previews need.
type gitConfig struct {
	pushMatching bool // push.default is matching
	remotePush   bool // some remote has its own push refspecs
}

// settings lists the repository's settings, which runs nothing, and returns an
// error when the repository itself (its own config, or a file it includes)
// sets a program for git to run. Settings from the user's own files, the
// environment or the command line are the user's choice and are allowed.
func (g gitQuery) settings() (gitConfig, error) {
	var c gitConfig
	text, err := g.out("config", "--list", "--show-origin", "--show-scope", "--includes", "-z")
	if err != nil {
		return c, err
	}
	recs := strings.Split(text, "\x00") // scope, origin, entry, scope, origin, entry...
	for i := 0; i+2 < len(recs); i += 3 {
		key, val, _ := strings.Cut(recs[i+2], "\n")
		key = strings.ToLower(key)
		if scope := recs[i]; (scope == "local" || scope == "worktree") && runsPrograms(key) {
			return c, errGitPrograms
		}
		switch {
		case key == "push.default":
			c.pushMatching = strings.EqualFold(val, "matching")
		case strings.HasPrefix(key, "remote.") && strings.HasSuffix(key, ".push"):
			c.remotePush = true
		}
	}
	return c, nil
}

// resolves reports whether rev names a commit.
func (g gitQuery) resolves(rev string) bool {
	_, err := g.out("rev-parse", "--verify", "--quiet", rev+"^{commit}")
	return err == nil
}

// gitArgs are the words after the git subcommand, split the way the rules read them.
type gitArgs struct {
	short map[rune]bool
	long  map[string]bool
	pos   []string // words before any --
	paths []string // words after --
}

func parseGitArgs(args []string) gitArgs {
	a := gitArgs{short: map[rune]bool{}, long: map[string]bool{}}
	dashed := false
	for _, w := range args {
		switch {
		case dashed:
			a.paths = append(a.paths, w)
		case w == "--":
			dashed = true
		case strings.HasPrefix(w, "--"):
			a.long[strings.SplitN(w[2:], "=", 2)[0]] = true
		case strings.HasPrefix(w, "-") && len(w) > 1:
			for _, r := range w[1:] {
				a.short[r] = true
			}
		default:
			a.pos = append(a.pos, w)
		}
	}
	return a
}

// InspectGit asks git itself what a destructive git command would lose: the
// changed or untracked files and the commits no other branch, tag or remote
// branch still has. It runs only read-only git commands in dir. When git
// cannot answer, it says so and the rule level stands.
func InspectGit(run GitRunner, dir string, cmds []shellparse.Command) []domain.Effect {
	for _, c := range cmds {
		if c.Name == "cd" || c.Name == "pushd" {
			return nil // the repository the later commands see is no longer known
		}
	}
	var out []domain.Effect
	changed := false // an earlier git command may already have changed the repository
	for _, c := range cmds {
		if c.Name != "git" || c.Remote != "" {
			continue
		}
		d, sub, rest, ok := gitSubcommand(dir, c.Args)
		if !ok {
			continue
		}
		if changed {
			return out
		}
		changed = !gitReads[sub]
		if !gitPreviewed[sub] {
			continue
		}
		g := gitQuery{run: run, dir: d}
		var es []domain.Effect
		cfg, err := g.settings()
		g.cfg = cfg
		var preview func([]string) ([]domain.Effect, error)
		switch sub {
		case "reset":
			preview = g.reset
		case "checkout", "restore":
			preview = func(r []string) ([]domain.Effect, error) { return g.discard(sub, r) }
		case "clean":
			preview = g.clean
		case "push":
			preview = g.push
		case "branch":
			preview = g.deleteBranch
		case "stash":
			preview = g.stash
		}
		if err == nil {
			es, err = preview(rest)
		}
		switch {
		case errors.Is(err, errGitPrograms):
			es = []domain.Effect{gitNote("this repository runs its own programs for git, so SafeShell did not look inside it")}
		case err != nil:
			es = []domain.Effect{gitNote("git could not say what this would lose (this may not be a git repository)")}
		}
		out = append(out, es...)
	}
	return out
}

// gitPreviewed are the git subcommands that have a preview.
var gitPreviewed = map[string]bool{"reset": true, "checkout": true, "restore": true, "clean": true, "push": true, "branch": true, "stash": true}

// gitReads are the git subcommands that change nothing.
var gitReads = map[string]bool{"status": true, "log": true, "diff": true, "show": true, "rev-parse": true, "rev-list": true,
	"ls-files": true, "blame": true, "describe": true, "grep": true, "shortlog": true, "ls-remote": true}

// gitNote is shown when git could not be asked. It is only an estimate and can
// never lower the level.
func gitNote(why string) domain.Effect {
	return domain.Effect{Kind: domain.EffectGit, Evidence: domain.Estimated, MinLevel: domain.R0, Summary: why + "; the rule level stands"}
}

// gitSubcommand skips git's own options (-C dir, -c name=value, --no-pager) and
// returns the folder to look in, the subcommand and its words. ok is false when
// the options point somewhere this cannot follow.
func gitSubcommand(base string, args []string) (d, sub string, rest []string, ok bool) {
	dir := base
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-C" && i+1 < len(args):
			i++
			dir = args[i]
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(base, dir)
			}
		case a == "-c" && i+1 < len(args):
			i++
		case strings.HasPrefix(a, "--git-dir") || strings.HasPrefix(a, "--work-tree") || strings.HasPrefix(a, "--namespace"):
			return "", "", nil, false
		case strings.HasPrefix(a, "-"):
		default:
			return dir, a, args[i+1:], true
		}
	}
	return "", "", nil, false
}

const gitListMax = 10

// gitLoss collects what a command would lose.
type gitLoss struct {
	files, untracked, commits []string
	moreCommits               bool
	notes                     []string
}

// effect turns a loss into an Effect. what names the command and verb says what
// happens to the items ("discard", "delete"...).
func (l gitLoss) effect(what, verb string, ev domain.Evidence) []domain.Effect {
	e := domain.Effect{Kind: domain.EffectGit, Evidence: ev, MinLevel: domain.R0}
	var parts []string
	if n := len(l.files); n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed file(s)", n))
	}
	if n := len(l.untracked); n > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked file(s)", n))
	}
	if n := len(l.commits); n > 0 {
		c := fmt.Sprintf("%d commit(s) that nothing else keeps", n)
		if l.moreCommits {
			c = fmt.Sprintf("more than %d commits that nothing else keeps", gitListMax)
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		e.Summary = what + ": nothing would be lost"
	} else {
		e.MinLevel = domain.R2
		e.Summary = fmt.Sprintf("%s would %s %s", what, verb, strings.Join(parts, " and "))
	}
	e.Details = append(e.Details, listed("changed ", l.files)...)
	e.Details = append(e.Details, listed("untracked ", l.untracked)...)
	e.Details = append(e.Details, listed("commit ", l.commits)...)
	e.Details = append(e.Details, l.notes...)
	return []domain.Effect{e}
}

// listed prefixes up to gitListMax items and says how many were left out.
// Names come from the repository, so they are made safe to print.
func listed(prefix string, items []string) []string {
	var out []string
	for i, it := range items {
		if i == gitListMax {
			out = append(out, fmt.Sprintf("... and %d more", len(items)-gitListMax))
			break
		}
		out = append(out, prefix+report.Printable(it))
	}
	return out
}

// commits lists up to gitListMax "hash subject" lines for the log arguments,
// and whether there are more.
func (g gitQuery) commits(logArgs ...string) (lines []string, more bool, err error) {
	args := append([]string{"log", "--format=%h %s", fmt.Sprintf("--max-count=%d", gitListMax+1)}, logArgs...)
	text, err := g.out(append(args, "--")...)
	if err != nil {
		return nil, false, err
	}
	lines = splitLines(text)
	if len(lines) > gitListMax {
		return lines[:gitListMax], true, nil
	}
	return lines, false, nil
}

func splitLines(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func splitNul(text string) []string {
	var out []string
	for _, l := range strings.Split(text, "\x00") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// elsewhere are log arguments, placed after --not, that stop counting commits
// some other branch, tag or remote branch still has. branch is the local branch
// being moved or deleted, which does not count as "elsewhere".
func elsewhere(branch string) []string {
	var args []string
	if branch != "" {
		args = append(args, "--exclude="+branch)
	}
	return append(args, "--branches", "--tags", "--remotes")
}

func safeName(s string) bool { return s != "" && !strings.HasPrefix(s, "-") }

// branch is the current local branch, or "" for a detached head, which belongs to no branch.
func (g gitQuery) branch() (string, error) {
	ref, err := g.out("rev-parse", "--symbolic-full-name", "HEAD")
	if err != nil {
		return "", err
	}
	b, _ := strings.CutPrefix(strings.TrimSpace(ref), "refs/heads/")
	if b == "HEAD" {
		b = ""
	}
	return b, nil
}

// reset: git reset --hard [target] throws away tracked changes, and the commits
// the branch leaves behind when nothing else points at them.
func (g gitQuery) reset(rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	if !a.long["hard"] {
		return nil, nil
	}
	text, err := g.out("status", "--porcelain=v1", "-z", "--untracked-files=no")
	if err != nil {
		return nil, err
	}
	var l gitLoss
	recs := splitNul(text)
	for i := 0; i < len(recs); i++ {
		if len(recs[i]) < 4 {
			continue
		}
		l.files = append(l.files, recs[i][3:])
		if recs[i][0] == 'R' || recs[i][0] == 'C' {
			i++ // the next record is the old name
		}
	}
	target := "HEAD"
	if len(a.pos) > 0 {
		target = a.pos[0]
	}
	if !safeName(target) || !g.resolves(target) {
		return nil, errGitNoTarget // never claim nothing is lost for a target that cannot be looked up
	}
	branch, err := g.branch()
	if err != nil {
		return nil, err
	}
	args := append([]string{"HEAD", "--not", target}, elsewhere(branch)...)
	if l.commits, l.moreCommits, err = g.commits(args...); err != nil {
		return nil, err
	}
	return l.effect("git reset --hard", "discard", domain.Inspected), nil
}

// discard: git checkout -- paths and git restore paths overwrite edited files with the saved version.
func (g gitQuery) discard(sub string, rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	var paths []string
	switch {
	case sub == "checkout" && len(a.pos) == 0:
		paths = a.paths
	case sub == "checkout" && len(a.pos) == 1 && a.pos[0] == "." && len(a.paths) == 0:
		paths = []string{"."}
	case sub == "restore" && !a.long["source"] && !a.short['s'] && !a.long["patch"] && !a.short['p'] &&
		(!(a.long["staged"] || a.short['S']) || a.long["worktree"] || a.short['W']):
		paths = append(a.pos, a.paths...)
	}
	if len(paths) == 0 {
		return nil, nil
	}
	// status never writes the index (diff may refresh it, which runs a hook and changes the repository).
	text, err := g.out(append([]string{"status", "--porcelain=v1", "-z", "--untracked-files=no", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	staged := sub == "restore" && (a.long["staged"] || a.short['S'])
	var l gitLoss
	recs := splitNul(text)
	for i := 0; i < len(recs); i++ {
		r := recs[i]
		if len(r) < 4 {
			continue
		}
		if r[1] != ' ' || (staged && r[0] != ' ') { // second column: working copy differs from the index; first: index differs from HEAD
			l.files = append(l.files, r[3:])
		}
		if r[0] == 'R' || r[0] == 'C' {
			i++ // the next record is the old name
		}
	}
	return l.effect("git "+sub, "discard", domain.Inspected), nil
}

// clean: git clean -f deletes untracked files; git's own dry run lists them.
func (g gitQuery) clean(rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	if a.short['n'] || a.long["dry-run"] || a.short['e'] || a.long["exclude"] || a.short['i'] || a.long["interactive"] {
		return nil, nil
	}
	args := []string{"clean", "-n"}
	if forces(rest) >= 2 { // forced twice, clean also removes folders that are repositories of their own
		args = append(args, "-ff")
	}
	for _, f := range []rune{'d', 'x', 'X'} {
		if a.short[f] {
			args = append(args, "-"+string(f))
		}
	}
	args = append(args, "--")
	args = append(args, a.pos...)
	args = append(args, a.paths...)
	text, err := g.out(args...)
	if err != nil {
		return nil, err
	}
	var l gitLoss
	for _, line := range splitLines(text) {
		if name, ok := strings.CutPrefix(line, "Would remove "); ok { // not "Would skip repository ..."
			l.untracked = append(l.untracked, name)
		}
	}
	return l.effect("git clean", "delete", domain.Previewed), nil
}

// forces counts how many times -f or --force is given before any --.
func forces(args []string) int {
	n := 0
	for _, w := range args {
		switch {
		case w == "--":
			return n
		case w == "--force":
			n++
		case strings.HasPrefix(w, "-") && !strings.HasPrefix(w, "--"):
			n += strings.Count(w, "f")
		}
	}
	return n
}

// push: a forced push replaces the remote branch, so the commits on it that
// you do not have are lost. Nothing is fetched; this reads the last-fetched state.
func (g gitQuery) push(rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	var specs []string
	remote := ""
	if words := append(a.pos, a.paths...); len(words) > 0 {
		remote, specs = words[0], words[1:]
	}
	if a.long["all"] || a.long["mirror"] || a.long["tags"] || a.long["prune"] {
		return nil, nil // these push more than one named branch
	}
	forced := a.short['f'] || a.long["force"] || a.long["force-with-lease"]
	for _, s := range specs {
		forced = forced || strings.HasPrefix(s, "+")
	}
	if !forced || (remote != "" && (!safeName(remote) || strings.ContainsAny(remote, "/:@"))) {
		return nil, nil
	}
	branch, err := g.branch()
	if err != nil {
		return nil, err
	}
	if len(specs) == 0 {
		if branch == "" || g.cfg.pushMatching || g.cfg.remotePush {
			return nil, nil // with these settings a bare push sends more than the current branch, or somewhere else
		}
		dst := branch
		if up, err := g.out("rev-parse", "--abbrev-ref", "@{upstream}"); err == nil {
			if r, b, ok := strings.Cut(strings.TrimSpace(up), "/"); ok && (remote == "" || remote == r) {
				remote, dst = r, b
			}
		}
		if remote == "" {
			return nil, errGitNoRemote
		}
		specs = []string{branch + ":" + dst}
	}
	var out []domain.Effect
	for _, s := range specs {
		src, dst, _ := strings.Cut(strings.TrimPrefix(s, "+"), ":")
		if dst == "" {
			dst = src
		}
		dst = strings.TrimPrefix(dst, "refs/heads/")
		if src == "HEAD" && branch != "" {
			src = branch // the branch HEAD is on is what git pushes (a detached HEAD pushes itself)
		}
		if dst == "HEAD" {
			dst = branch
		}
		track := "refs/remotes/" + remote + "/" + dst
		if remote == "" || !safeName(src) || !safeName(dst) || strings.ContainsAny(src+dst, "*?[") || !g.resolves(src) || !g.resolves(track) {
			continue
		}
		var l gitLoss
		if l.commits, l.moreCommits, err = g.commits(track, "--not", src); err != nil {
			return nil, err
		}
		l.notes = []string{"based on your last fetch of " + report.Printable(remote)}
		e := l.effect("git push --force to "+report.Printable(remote+"/"+dst), "overwrite", domain.Inspected)
		if e[0].MinLevel == domain.R0 {
			e[0].Summary = "git push --force to " + report.Printable(remote+"/"+dst) + ": nothing your last fetch saw would be lost"
		}
		out = append(out, e...)
	}
	return out, nil
}

// deleteBranch: git branch -D drops a branch even if its commits are merged nowhere.
func (g gitQuery) deleteBranch(rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	forced := a.short['D'] || ((a.short['d'] || a.long["delete"]) && (a.short['f'] || a.long["force"]))
	if !forced || a.short['r'] || a.long["remotes"] {
		return nil, nil
	}
	var out []domain.Effect
	for _, name := range append(a.pos, a.paths...) {
		ref := "refs/heads/" + name
		if !safeName(name) || !g.resolves(ref) {
			continue
		}
		var l gitLoss
		var err error
		args := append([]string{ref, "--not", "HEAD"}, elsewhere(name)...)
		if l.commits, l.moreCommits, err = g.commits(args...); err != nil {
			return nil, err
		}
		out = append(out, l.effect("git branch -D "+report.Printable(name), "delete", domain.Inspected)...)
	}
	return out, nil
}

var (
	stashRef = regexp.MustCompile(`^stash@\{[0-9]+\}$`)
	stashNum = regexp.MustCompile(`^[0-9]+$`)
)

// stash: drop deletes one saved stash and clear deletes them all.
func (g gitQuery) stash(rest []string) ([]domain.Effect, error) {
	a := parseGitArgs(rest)
	if len(a.pos) == 0 || (a.pos[0] != "drop" && a.pos[0] != "clear") {
		return nil, nil
	}
	text, err := g.out("stash", "list")
	if err != nil {
		return nil, err
	}
	entries := splitLines(text)
	if a.pos[0] == "drop" {
		ref := "stash@{0}"
		if len(a.pos) > 1 && stashRef.MatchString(a.pos[1]) {
			ref = a.pos[1]
		} else if len(a.pos) > 1 && stashNum.MatchString(a.pos[1]) {
			ref = "stash@{" + a.pos[1] + "}"
		}
		var one []string
		for _, e := range entries {
			if strings.HasPrefix(e, ref+":") {
				one = append(one, e)
			}
		}
		entries = one
	}
	e := domain.Effect{Kind: domain.EffectGit, Evidence: domain.Inspected, MinLevel: domain.R0,
		Summary: "git stash " + a.pos[0] + ": no saved stash would be lost"}
	if len(entries) > 0 {
		e.MinLevel = domain.R2
		e.Summary = fmt.Sprintf("git stash %s would delete %d saved stash(es) for good", a.pos[0], len(entries))
		e.Details = listed("", entries)
	}
	return []domain.Effect{e}, nil
}
