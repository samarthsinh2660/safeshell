package effects

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// testGit runs real git in dir with no user or system settings.
func testGit(dir string, args ...string) (string, error) {
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "GIT_NO_LAZY_FETCH=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "LC_ALL=C")
	out, err := c.Output()
	return string(out), err
}

type repo struct {
	t   *testing.T
	dir string
}

func (r repo) git(args ...string) string {
	r.t.Helper()
	out, err := testGit(r.dir, args...)
	if err != nil {
		r.t.Fatalf("git %v: %v %s", args, err, err.(*exec.ExitError).Stderr)
	}
	return out
}

func (r repo) write(name, text string) {
	r.t.Helper()
	p := filepath.Join(r.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r repo) commit(name, text, msg string) {
	r.t.Helper()
	r.write(name, text)
	r.git("add", name)
	r.git("commit", "-q", "-m", msg)
}

// newRepo makes a repository on main with one commit, a remote called origin
// that has it, and an upstream set.
func newRepo(t *testing.T) repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	r := repo{t, filepath.Join(root, "work")}
	if err := os.Mkdir(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	remote := filepath.Join(root, "remote.git")
	if _, err := testGit(root, "init", "-q", "--bare", "-b", "main", remote); err != nil {
		t.Fatal(err)
	}
	r.git("init", "-q", "-b", "main")
	r.commit("a.txt", "one\n", "first")
	r.commit("sub/c.txt", "c\n", "second")
	r.git("remote", "add", "origin", remote)
	r.git("push", "-q", "-u", "origin", "main")
	return r
}

func (r repo) inspect(line string) []domain.Effect {
	r.t.Helper()
	return InspectGit(testGit, r.dir, cmds(r.t, line))
}

func (r repo) one(line string) domain.Effect {
	r.t.Helper()
	es := r.inspect(line)
	if len(es) != 1 {
		r.t.Fatalf("%s: want one effect, got %+v", line, es)
	}
	return es[0]
}

func (r repo) expect(line string, ev domain.Evidence, min domain.Level, summary string, details ...string) {
	r.t.Helper()
	e := r.one(line)
	all := strings.Join(e.Details, "\n")
	if e.Kind != domain.EffectGit || e.Evidence != ev || e.MinLevel != min || !strings.Contains(e.Summary, summary) {
		r.t.Errorf("%s: %+v", line, e)
	}
	for _, d := range details {
		if !strings.Contains(all, d) {
			r.t.Errorf("%s: missing %q in %q", line, d, all)
		}
	}
}

func TestGitResetHardListsChangedFilesAndStrandedCommits(t *testing.T) {
	r := newRepo(t)
	r.expect("git reset --hard", domain.Inspected, domain.R0, "nothing would be lost")

	r.write("a.txt", "edited\n")
	r.write("new.txt", "staged\n")
	r.git("add", "new.txt")
	r.write("untracked.txt", "x\n")
	r.git("mv", "sub/c.txt", "sub/d.txt")
	e := r.one("git reset --hard")
	if e.MinLevel != domain.R2 || !strings.Contains(e.Summary, "3 changed file(s)") || strings.Contains(strings.Join(e.Details, " "), "untracked.txt") {
		t.Errorf("%+v", e)
	}
	r.expect("git reset --hard", domain.Inspected, domain.R2, "discard", "changed a.txt", "changed new.txt", "changed sub/d.txt")
	r.git("reset", "-q", "--hard")

	r.commit("late.txt", "late\n", "late work")
	r.git("push", "-q", "origin", "main")
	// The commit is also on origin/main, so nothing is stranded.
	r.expect("git reset --hard HEAD~1", domain.Inspected, domain.R0, "nothing would be lost")
	r.commit("only-here.txt", "x\n", "only here")
	r.expect("git reset --hard HEAD~1", domain.Inspected, domain.R2, "1 commit(s) that nothing else keeps", "commit ")
	r.expect("git reset --hard HEAD~1", domain.Inspected, domain.R2, "discard", "only here")
	r.git("branch", "keep")
	r.expect("git reset --hard HEAD~1", domain.Inspected, domain.R0, "nothing would be lost")

	// A detached head keeps no branch of its own.
	r.git("checkout", "-q", "--detach")
	r.git("branch", "-D", "keep")
	r.expect("git reset --hard main~3", domain.Inspected, domain.R0, "nothing would be lost")
	r.commit("det.txt", "d\n", "detached work")
	r.expect("git reset --hard HEAD~1", domain.Inspected, domain.R2, "1 commit(s)", "detached work")

	// Not a hard reset, an unknown target, and a flag-like target are left to the rules.
	if es := r.inspect("git reset --soft HEAD~1"); es != nil {
		t.Errorf("%+v", es)
	}
	r.expect("git reset --hard --keep", domain.Inspected, domain.R0, "nothing would be lost")
}

func TestGitResetWithATargetItCannotLookUpIsNeverReportedAsSafe(t *testing.T) {
	r := newRepo(t)
	for _, line := range []string{"git reset --hard nosuchbranch", "git reset --hard $(git rev-parse HEAD~1)", "git reset --hard $REV"} {
		if es := r.inspect(line); len(es) != 1 || es[0].Evidence != domain.Estimated || es[0].MinLevel != domain.R0 || !strings.Contains(es[0].Summary, "rule level stands") {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestGitResetListsAtMostTenOfManyCommits(t *testing.T) {
	r := newRepo(t)
	r.git("checkout", "-q", "-b", "work")
	for i := 0; i < 12; i++ {
		r.commit(fmt.Sprintf("f%d.txt", i), "x\n", fmt.Sprintf("c%d", i))
		r.write(fmt.Sprintf("f%d.txt", i), "edited\n")
	}
	e := r.one("git reset --hard main")
	if !strings.Contains(e.Summary, "12 changed file(s) and more than 10 commits") {
		t.Errorf("%+v", e)
	}
	if !strings.Contains(strings.Join(e.Details, "\n"), "... and 2 more") || len(e.Details) != 21 {
		t.Errorf("%d details: %q", len(e.Details), e.Details)
	}
}

func TestGitPrintsRepositoryNamesSafely(t *testing.T) {
	r := newRepo(t)
	r.commit("bad\x1b[31m.txt", "x\n", "hostile")
	r.write("bad\x1b[31m.txt", "y\n")
	e := r.one("git reset --hard")
	if all := strings.Join(e.Details, "\n"); strings.Contains(all, "\x1b") || !strings.Contains(all, `\x1b`) {
		t.Errorf("%q", all)
	}
}

func TestGitCheckoutAndRestoreListTheEditsTheyThrowAway(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "edited\n")
	r.write("sub/c.txt", "edited\n")
	r.write("fresh.txt", "untracked\n")
	r.expect("git checkout -- a.txt", domain.Inspected, domain.R2, "git checkout would discard 1 changed", "changed a.txt")
	r.expect("git checkout .", domain.Inspected, domain.R2, "2 changed file(s)", "changed sub/c.txt")
	r.expect("git restore sub", domain.Inspected, domain.R2, "1 changed")
	r.git("reset", "-q")
	r.git("add", "a.txt")
	r.expect("git restore --worktree --staged a.txt", domain.Inspected, domain.R2, "1 changed", "changed a.txt")
	r.expect("git restore fresh.txt", domain.Inspected, domain.R0, "nothing would be lost")
	for _, line := range []string{
		"git checkout main", "git checkout HEAD -- a.txt", "git checkout . a.txt", "git checkout --", "git restore",
		"git restore --staged a.txt", "git restore --source=HEAD~1 a.txt", "git restore -s HEAD~1 a.txt", "git restore -p a.txt", "git restore --patch a.txt",
	} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestGitCleanUsesGitsOwnDryRun(t *testing.T) {
	r := newRepo(t)
	r.expect("git clean -fd", domain.Previewed, domain.R0, "nothing would be lost")
	r.write("junk.txt", "x\n")
	r.write("build/out.o", "x\n")
	r.write(".gitignore", "*.log\n")
	r.git("add", ".gitignore")
	r.git("commit", "-q", "-m", "ignore")
	r.write("debug.log", "x\n")
	r.expect("git clean -f", domain.Previewed, domain.R2, "git clean would delete 1 untracked file(s)", "untracked junk.txt")
	r.expect("git clean -fd", domain.Previewed, domain.R2, "2 untracked", "untracked build/")
	r.expect("git clean -fdx", domain.Previewed, domain.R2, "3 untracked", "untracked debug.log")
	r.expect("git clean -fdX", domain.Previewed, domain.R2, "1 untracked", "untracked debug.log")
	r.expect("git clean -f -d -- build", domain.Previewed, domain.R2, "1 untracked", "untracked build/")
	r.expect("git clean -fd build", domain.Previewed, domain.R2, "1 untracked")
	for _, line := range []string{"git clean -n", "git clean -fdn", "git clean --dry-run", "git clean -fd -e junk.txt", "git clean -fdi"} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestGitPushForceListsRemoteCommitsYouWouldOverwrite(t *testing.T) {
	r := newRepo(t)
	r.expect("git push --force", domain.Inspected, domain.R0, "nothing your last fetch saw would be lost")
	// Someone else's commit reaches the remote and this clone fetches it.
	other := filepath.Join(t.TempDir(), "other")
	remote := strings.TrimSpace(r.git("remote", "get-url", "origin"))
	if _, err := testGit(filepath.Dir(other), "clone", "-q", remote, other); err != nil {
		t.Fatal(err)
	}
	o := repo{t, other}
	o.commit("theirs.txt", "t\n", "their work")
	o.git("push", "-q", "origin", "main")
	r.git("fetch", "-q", "origin")
	for _, line := range []string{"git push --force", "git push -f origin HEAD", "git push -f origin HEAD:main", "git push -f origin main:HEAD", "git push -f origin main", "git push origin +main", "git push --force-with-lease origin main:main", "git push -f origin HEAD:refs/heads/main"} {
		r.expect(line, domain.Inspected, domain.R2, "git push --force to origin/main would overwrite 1 commit(s)", "commit ", "their work", "last fetch of origin")
	}
	for _, line := range []string{
		"git push", "git push origin main", "git push -f origin :main", "git push -f origin nosuch", "git push -f origin 'a*'",
		"git push -f " + remote, "git push -f ssh://h/p main", "git push -f origin/x main", "git push -f -- main",
	} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	r.git("branch", "--unset-upstream")
	r.expect("git push -f origin", domain.Inspected, domain.R2, "overwrite 1 commit(s)")
	if es := r.inspect("git push -f"); len(es) != 1 || es[0].Evidence != domain.Estimated || es[0].MinLevel != domain.R0 {
		t.Errorf("no remote known: %+v", es)
	}
	r.git("checkout", "-q", "--detach")
	for _, line := range []string{"git push -f origin", "git push -f origin HEAD", "git push -f origin main:HEAD"} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	if es := r.inspect("git push -f origin"); es != nil {
		t.Errorf("%+v", es)
	}
}

func TestGitBranchDeleteListsUnmergedCommits(t *testing.T) {
	r := newRepo(t)
	r.git("branch", "merged")
	r.git("checkout", "-q", "-b", "side")
	r.commit("s.txt", "s\n", "side work")
	r.git("checkout", "-q", "main")
	r.expect("git branch -D merged", domain.Inspected, domain.R0, "nothing would be lost")
	r.expect("git branch -D side", domain.Inspected, domain.R2, "delete 1 commit(s)", "side work")
	r.expect("git branch -df side", domain.Inspected, domain.R2, "1 commit(s)")
	r.expect("git branch --delete --force side", domain.Inspected, domain.R2, "1 commit(s)")
	if es := r.inspect("git branch -D side merged"); len(es) != 2 {
		t.Errorf("%+v", es)
	}
	r.git("branch", "keeper", "side")
	r.expect("git branch -D side", domain.Inspected, domain.R0, "nothing would be lost")
	for _, line := range []string{"git branch -d side", "git branch", "git branch -D", "git branch -D nosuch", "git branch -D -- -x", "git branch -rD origin/main"} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestGitStashDropAndClearListTheStashes(t *testing.T) {
	r := newRepo(t)
	r.expect("git stash drop", domain.Inspected, domain.R0, "no saved stash")
	for _, msg := range []string{"first idea", "second idea"} {
		r.write("a.txt", msg+"\n")
		r.git("stash", "push", "-q", "-m", msg)
	}
	r.expect("git stash drop", domain.Inspected, domain.R2, "delete 1 saved stash", "second idea")
	r.expect("git stash drop stash@{1}", domain.Inspected, domain.R2, "delete 1 saved stash", "first idea")
	r.expect("git stash drop 1", domain.Inspected, domain.R2, "delete 1 saved stash", "first idea")
	r.expect("git stash drop stash@{9}", domain.Inspected, domain.R0, "no saved stash")
	r.expect("git stash drop ; rm", domain.Inspected, domain.R2, "delete 1 saved stash", "second idea")
	r.expect("git stash clear", domain.Inspected, domain.R2, "clear would delete 2 saved stash", "first idea", "second idea")
	for _, line := range []string{"git stash", "git stash list", "git stash pop"} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
}

func TestGitFollowsDashCAndSkipsWhatItCannotFollow(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "edited\n")
	here := InspectGit(testGit, filepath.Dir(r.dir), cmds(t, "git -C work -c core.quotepath=off --no-pager reset --hard"))
	if len(here) != 1 || here[0].MinLevel != domain.R2 {
		t.Errorf("-C relative: %+v", here)
	}
	abs := InspectGit(testGit, t.TempDir(), cmds(t, "git -C "+r.dir+" reset --hard"))
	if len(abs) != 1 || abs[0].MinLevel != domain.R2 {
		t.Errorf("-C absolute: %+v", abs)
	}
	for _, line := range []string{
		"cd /tmp && git reset --hard", "git --git-dir=/x reset --hard", "git --work-tree /x reset --hard", "git -C", "git", "git -c x=y",
		"git status", "echo git reset --hard", "git log -n1",
	} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	remote := []shellparse.Command{{Name: "git", Args: []string{"reset", "--hard"}, Remote: "host"}}
	if es := InspectGit(testGit, r.dir, remote); es != nil {
		t.Errorf("remote: %+v", es)
	}
}

func TestGitOutsideARepositoryKeepsTheRuleLevel(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	es := InspectGit(testGit, t.TempDir(), cmds(t, "git reset --hard"))
	if len(es) != 1 || es[0].Evidence != domain.Estimated || es[0].MinLevel != domain.R0 || !strings.Contains(es[0].Summary, "rule level stands") {
		t.Errorf("%+v", es)
	}
}

func TestGitOnlyRunsReadOnlyCommands(t *testing.T) {
	var ran []string
	g := gitQuery{run: func(dir string, args ...string) (string, error) {
		if args[0] == "config" { // listing settings runs nothing, so it carries no options
			ran = append(ran, "config")
			return "", nil
		}
		if !strings.HasPrefix(strings.Join(args, " "), strings.Join(gitSafeArgs, " ")) {
			t.Errorf("a query without the safety options: %v", args)
		}
		ran = append(ran, args[len(gitSafeArgs)])
		return "", nil
	}}
	for _, args := range [][]string{{"reset", "--hard"}, {"stash", "drop"}, {"stash"}, {"clean", "-f"}, {"clean"}, {"push"}, {"checkout", "."}, {"branch", "-D", "x"}, {"config", "--get", "x"}, {"config"}, {"diff"}} {
		if _, err := g.out(args...); !errors.Is(err, errGitNotAllowed) {
			t.Errorf("%v was not refused: %v", args, err)
		}
	}
	for _, args := range [][]string{{"status"}, {"rev-parse"}, {"log"}, {"stash", "list"}, {"clean", "-n"}, {"config", "--list"}} {
		if _, err := g.out(args...); err != nil {
			t.Errorf("%v: %v", args, err)
		}
	}
	if len(ran) != 6 {
		t.Errorf("ran %v", ran)
	}
}

// TestGitFailuresKeepTheRuleLevel makes each query in turn fail and checks
// the command is never reported as safe.
func TestGitFailuresKeepTheRuleLevel(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "edited\n")
	r.git("stash", "push", "-q")
	r.write("a.txt", "edited\n")
	r.write("junk.txt", "x\n")
	r.git("checkout", "-q", "-b", "side")
	r.commit("s.txt", "s\n", "side")
	r.git("checkout", "-q", "main")
	for _, line := range []string{
		"git reset --hard side", "git checkout -- a.txt", "git clean -f", "git push -f", "git push -f origin side", "git branch -D side", "git stash drop",
	} {
		calls := 0
		count := func(dir string, args ...string) (string, error) { calls++; return testGit(dir, args...) }
		InspectGit(count, r.dir, cmds(t, line))
		for k := 1; k <= calls; k++ {
			n := 0
			failing := func(dir string, args ...string) (string, error) {
				if n++; n == k {
					return "", errors.New("boom")
				}
				return testGit(dir, args...)
			}
			for _, e := range InspectGit(failing, r.dir, cmds(t, line)) {
				if k == 1 && e.Evidence != domain.Estimated {
					t.Errorf("%s with its first query failing claimed %+v", line, e)
				}
			}
		}
	}
}

func TestGitResetIgnoresStatusRecordsItDoesNotUnderstand(t *testing.T) {
	run := func(dir string, args ...string) (string, error) {
		if args[0] == "config" {
			return "", nil
		}
		if args[len(gitSafeArgs)] == "status" {
			return "x\x00 M a.txt\x00", nil
		}
		return "", nil
	}
	es := InspectGit(run, "/repo", cmds(t, "git reset --hard HEAD"))
	if len(es) != 1 || !strings.Contains(es[0].Summary, "discard 1 changed file(s)") {
		t.Errorf("%+v", es)
	}
}

func TestGitCleanSkipsNestedRepositoriesUnlessForcedTwice(t *testing.T) {
	r := newRepo(t)
	nested := filepath.Join(r.dir, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := testGit(nested, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	r.expect("git clean -fd", domain.Previewed, domain.R0, "nothing would be lost")
	r.expect("git clean -ffd", domain.Previewed, domain.R2, "1 untracked", "untracked nested/")
	r.expect("git clean --force --force -d", domain.Previewed, domain.R2, "1 untracked", "untracked nested/")
	r.expect("git clean -f -d -- -ff", domain.Previewed, domain.R0, "nothing would be lost")
	r.expect("git clean --force --force -d", domain.Previewed, domain.R2, "1 untracked", "untracked nested/")
}

func TestGitStopsPreviewingAfterAnEarlierCommandChangedTheRepository(t *testing.T) {
	r := newRepo(t)
	r.write("a.txt", "edited\n")
	for _, line := range []string{"git checkout main && git reset --hard", "git stash drop && git stash drop", "git commit -m x ; git reset --hard"} {
		if es := r.inspect(line); len(es) > 1 || (len(es) == 1 && line != "git stash drop && git stash drop") {
			t.Errorf("%s: %+v", line, es)
		}
	}
	if es := r.inspect("git status && git log -n1 && git reset --hard"); len(es) != 1 || es[0].MinLevel != domain.R2 {
		t.Errorf("%+v", es)
	}
}

// markerRepo is a repository whose own settings would run marker scripts.
func markerRepo(t *testing.T, setting func(r repo, script string)) (repo, string) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	setting(r, script)
	r.write("a.txt", "edited\n")
	return r, marker
}

func TestGitRefusesProgramsTheRepositoryItselfSetsAndNeverRunsThem(t *testing.T) {
	settings := map[string]func(r repo, script string){
		"filter": func(r repo, script string) {
			r.git("config", "filter.x.clean", script)
			r.write(".gitattributes", "*.txt filter=x\n")
		},
		"filter from an included file": func(r repo, script string) {
			r.write("inc.cfg", "[filter \"x\"]\n\tclean = "+script+"\n")
			r.git("config", "include.path", "../inc.cfg")
			r.write(".gitattributes", "*.txt filter=x\n")
		},
		"textconv":      func(r repo, script string) { r.git("config", "diff.x.textconv", script) },
		"external diff": func(r repo, script string) { r.git("config", "diff.external", script) },
	}
	for name, set := range settings {
		t.Run(name, func(t *testing.T) {
			r, marker := markerRepo(t, set)
			for _, line := range []string{"git reset --hard", "git push -f", "git branch -D main", "git stash drop", "git clean -fd", "git checkout -- a.txt"} {
				es := r.inspect(line)
				if len(es) != 1 || es[0].Evidence != domain.Estimated || es[0].MinLevel != domain.R0 || !strings.Contains(es[0].Summary, "runs its own programs for git") {
					t.Errorf("%s: %+v", line, es)
				}
			}
			if _, err := os.Stat(marker); err == nil {
				t.Error("the repository's program ran")
			}
		})
	}
}

func TestGitOverridesSettingsThatCanBeSwitchedOffInsteadOfRefusing(t *testing.T) {
	settings := map[string]func(r repo, script string){
		"fsmonitor":      func(r repo, script string) { r.git("config", "core.fsmonitor", script) },
		"fsmonitor true": func(r repo, script string) { r.git("config", "core.fsmonitor", "true") },
		"gpg program": func(r repo, script string) {
			r.git("config", "gpg.program", script)
			r.git("config", "log.showSignature", "true")
		},
		"gpg format program": func(r repo, script string) { r.git("config", "gpg.ssh.program", script) },
		"hooks path": func(r repo, script string) {
			dir := filepath.Dir(script)
			if err := os.WriteFile(filepath.Join(dir, "post-index-change"), []byte("#!/bin/sh\ntouch "+filepath.Join(dir, "hooked")+"\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			r.git("config", "core.hooksPath", dir)
		},
	}
	for name, set := range settings {
		t.Run(name, func(t *testing.T) {
			r, marker := markerRepo(t, set)
			es := r.inspect("git reset --hard")
			if len(es) != 1 || es[0].Evidence != domain.Inspected || es[0].MinLevel != domain.R2 {
				t.Errorf("%+v", es)
			}
			for _, m := range []string{marker, filepath.Join(filepath.Dir(marker), "hooked")} {
				if _, err := os.Stat(m); err == nil {
					t.Errorf("%s: the repository's program ran", name)
				}
			}
		})
	}
}

// userConfigRunner runs git like testGit but with the given file as the user's own settings.
func userConfigRunner(file string, extraEnv ...string) GitRunner {
	return func(dir string, args ...string) (string, error) {
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+file, "GIT_CONFIG_NOSYSTEM=1", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
		c.Env = append(c.Env, extraEnv...)
		out, err := c.Output()
		return string(out), err
	}
}

func TestGitAllowsProgramsFromTheUsersOwnSettings(t *testing.T) {
	r := newRepo(t)
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[filter \"lfs\"]\n\tclean = cat\n[diff \"x\"]\n\ttextconv = cat\n[diff]\n\texternal = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.write("a.txt", "edited\n")
	for name, run := range map[string]GitRunner{
		"global file":   userConfigRunner(global),
		"command scope": userConfigRunner("/dev/null", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=filter.q.clean", "GIT_CONFIG_VALUE_0=cat"),
	} {
		es := InspectGit(run, r.dir, cmds(t, "git reset --hard"))
		if len(es) != 1 || es[0].Evidence != domain.Inspected || es[0].MinLevel != domain.R2 {
			t.Errorf("%s: %+v", name, es)
		}
	}
}

func TestGitDoesNotEnterSubmodulesWhoseSettingsItNeverChecked(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "filter.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	subSrc := repo{t, filepath.Join(t.TempDir(), "subsrc")}
	if err := os.Mkdir(subSrc.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	subSrc.git("init", "-q", "-b", "main")
	subSrc.write(".gitattributes", "*.txt filter=x\n")
	subSrc.git("add", ".gitattributes")
	subSrc.commit("s.txt", "s\n", "sub first")
	r.git("-c", "protocol.file.allow=always", "submodule", "add", "-q", subSrc.dir, "child")
	r.git("commit", "-q", "-m", "add submodule")
	modules := strings.TrimSpace(r.git("rev-parse", "--git-path", "modules/child/config"))
	cfg, err := os.OpenFile(filepath.Join(r.dir, modules), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.WriteString("[filter \"x\"]\n\tclean = " + script + "\n"); err != nil {
		t.Fatal(err)
	}
	cfg.Close()
	r.write("child/s.txt", "t\n") // same size, newer time: git has to run the filter to compare
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(r.dir, "child", "s.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	r.write("a.txt", "edited\n")
	// Control: a plain status enters the submodule and runs its filter.
	r.git("status", "--porcelain")
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run the submodule's filter for status")
	}
	os.Remove(marker)
	for _, line := range []string{"git reset --hard", "git checkout -- a.txt", "git checkout .", "git restore --staged --worktree ."} {
		if es := r.inspect(line); len(es) != 1 || es[0].Evidence != domain.Inspected {
			t.Errorf("%s: %+v", line, es)
		}
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a preview ran the submodule's filter")
	}
}

func TestGitPreviewsNeverWriteTheIndexOrRunItsHook(t *testing.T) {
	r := newRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := filepath.Join(r.dir, ".git", "hooks", "post-index-change")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour)
	touch := func() { // a newer timestamp, same content: a refresh would rewrite the index
		if err := os.Chtimes(filepath.Join(r.dir, "a.txt"), later, later); err != nil {
			t.Fatal(err)
		}
	}
	touch()
	r.write("sub/c.txt", "edited\n")
	r.git("mv", "a.txt", "renamed.txt")
	later = later.Add(time.Hour)
	if err := os.Chtimes(filepath.Join(r.dir, "renamed.txt"), later, later); err != nil {
		t.Fatal(err)
	}
	// Control: a plain diff refreshes the index and runs the hook.
	exec.Command("git", "-C", r.dir, "diff", "--name-only").Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run the hook when diff refreshes the index")
	}
	os.Remove(marker)
	if err := os.Chtimes(filepath.Join(r.dir, "renamed.txt"), later.Add(time.Hour), later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(r.dir, ".git", "index")
	before, _ := os.ReadFile(idx)
	beforeInfo, _ := os.Stat(idx)
	for _, line := range []string{"git checkout -- renamed.txt", "git checkout .", "git restore --staged --worktree .", "git reset --hard", "git clean -fd"} {
		if es := r.inspect(line); len(es) != 1 || es[0].Evidence == domain.Estimated {
			t.Errorf("%s: %+v", line, es)
		}
	}
	after, _ := os.ReadFile(idx)
	afterInfo, _ := os.Stat(idx)
	if !bytes.Equal(before, after) || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Error("a preview changed the index")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a preview ran the post-index-change hook")
	}
}

func TestGitPreviewsNeverFetchFromAPartialCloneRemote(t *testing.T) {
	r := newRepo(t)
	remote := strings.TrimSpace(r.git("remote", "get-url", "origin"))
	for _, kv := range [][]string{{"uploadpack.allowFilter", "true"}, {"uploadpack.allowAnySHA1InWant", "true"}} {
		if _, err := testGit(remote, "config", kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	clone := filepath.Join(t.TempDir(), "partial")
	if _, err := testGit(filepath.Dir(clone), "clone", "-q", "--no-checkout", "--filter=blob:none", "file://"+remote, clone); err != nil {
		t.Skip("this git cannot make a partial clone here")
	}
	p := repo{t, clone}
	blob := strings.TrimSpace(p.git("rev-parse", "HEAD:a.txt"))
	marker := filepath.Join(t.TempDir(), "ssh-ran")
	script := filepath.Join(t.TempDir(), "ssh.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	p.git("config", "remote.origin.url", "ssh://nowhere.invalid/x")
	p.git("config", "core.sshCommand", script)
	// Control: plain git tries to fetch the missing object through the ssh command.
	exec.Command("git", "-C", clone, "cat-file", "-p", blob).Run()
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not fetch missing objects here")
	}
	os.Remove(marker)
	if _, err := (gitQuery{run: testGit, dir: clone}).out("rev-parse", "--verify", "--quiet", blob+"^{blob}"); err == nil {
		t.Error("the object is missing, so this must not succeed")
	}
	for _, line := range []string{"git reset --hard", "git checkout .", "git clean -fd", "git push -f", "git branch -D main", "git stash drop"} {
		p.inspect(line)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a query reached for the network")
	}
}

func TestGitPushPreviewSkipsPushesThatSendMoreThanOneBranch(t *testing.T) {
	r := newRepo(t)
	for _, line := range []string{"git push -f --all", "git push -f --mirror", "git push -f --tags", "git push -f --prune origin main", "git push -f origin --all"} {
		if es := r.inspect(line); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	r.git("config", "push.default", "matching")
	if es := r.inspect("git push -f"); es != nil {
		t.Errorf("matching: %+v", es)
	}
	r.expect("git push -f origin main", domain.Inspected, domain.R0, "nothing your last fetch saw")
	r.git("config", "push.default", "simple")
	r.expect("git push -f", domain.Inspected, domain.R0, "nothing your last fetch saw")
	r.git("config", "remote.origin.push", "refs/heads/*:refs/heads/other/*")
	if es := r.inspect("git push -f"); es != nil {
		t.Errorf("remote push refspecs: %+v", es)
	}
	r.expect("git push -f origin main", domain.Inspected, domain.R0, "nothing your last fetch saw")
}

func TestGitForcePushFromADetachedHeadUsesHeadAsTheSource(t *testing.T) {
	r := newRepo(t)
	r.git("checkout", "-q", "--detach")
	r.expect("git push -f origin HEAD:main", domain.Inspected, domain.R0, "git push --force to origin/main")
}

func TestGitRestoreOfRenamedAndStagedFiles(t *testing.T) {
	r := newRepo(t)
	r.git("mv", "a.txt", "b.txt")
	r.write("b.txt", "edited\n")
	r.expect("git restore --staged --worktree .", domain.Inspected, domain.R2, "discard 1 changed", "changed b.txt")
	r.expect("git restore .", domain.Inspected, domain.R2, "discard 1 changed", "changed b.txt")
	r.git("add", "b.txt")
	r.expect("git restore .", domain.Inspected, domain.R0, "nothing would be lost")
	r.expect("git restore --staged -W .", domain.Inspected, domain.R2, "discard 2 changed", "changed b.txt") // the add made it a delete and a new file
}

func TestGitQueriesTurnOffTheFileSystemMonitor(t *testing.T) {
	r, marker := markerRepo(t, func(r repo, script string) { r.git("config", "core.fsmonitor", script) })
	// Control: plain git runs the program, so the check below means something.
	r.git("status")
	if _, err := os.Stat(marker); err != nil {
		t.Skip("this git does not run the monitor here")
	}
	os.Remove(marker)
	if _, err := (gitQuery{run: testGit, dir: r.dir}).out("status", "--porcelain=v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a query ran the file system monitor")
	}
}

func TestGitDiscardIgnoresStatusRecordsItDoesNotUnderstand(t *testing.T) {
	run := func(dir string, args ...string) (string, error) {
		if args[0] == "config" {
			return "", nil
		}
		return "x\x00 M a.txt\x00", nil
	}
	es := InspectGit(run, "/repo", cmds(t, "git checkout -- a.txt"))
	if len(es) != 1 || !strings.Contains(es[0].Summary, "discard 1 changed file(s)") {
		t.Errorf("%+v", es)
	}
}
