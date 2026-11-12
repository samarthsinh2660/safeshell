package rules

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func ruleGit(c shellparse.Command, f flags) []Finding {
	switch f.sub() {
	case "push":
		switch {
		case f.hasAny("--force-with-lease") || f.long["force-with-lease"] != "" || f.has(0, "force-with-lease"):
			return one(fnd(R2, c, "Rewrites history on the remote, but refuses if someone else pushed in the meantime."))
		case f.has('f', "force") || f.hasAny("--delete", "-d") || anyPrefix(f.pos, ":"):
			return one(fnd(R3, c, "Overwrites or deletes history on the shared remote. Other people's work can be lost.").
				WithAlt(strings.Replace(c.String(), "--force", "--force-with-lease", 1), "refuses if someone else pushed in the meantime"))
		}
		return one(fnd(R2, c, "Publishes commits to a shared remote. Hard to take back once others pull them."))
	case "reset":
		if f.hasAny("--hard") {
			return one(fnd(R3, c, "Throws away all uncommitted changes for good. Committed work stays recoverable through the reflog, but edits that were never committed cannot be brought back.").
				WithAlt("git stash", "saves your changes first so they can be brought back"))
		}
		if len(f.pos) > 1 && strings.ContainsAny(f.pos[len(f.pos)-1], "~^") { // git reset HEAD~1: rewinds history
			return one(fnd(R2, c, "Moves the branch back and rewrites its history. The commits stay in the reflog."))
		}
		return one(fnd(R1, c, "Moves the branch pointer or unstages files. Files are kept."))
	case "clean":
		if f.has('n', "dry-run") {
			return one(fnd(R0, c, "Only lists the untracked files it would remove."))
		}
		if f.has('f', "force") {
			return one(fnd(R3, c, "Deletes untracked files that git does not track, so they cannot be restored.").
				WithAlt("git clean -n"+strings.TrimPrefix(c.String(), "git clean"), "a dry run that lists what would be deleted"))
		}
		return one(fnd(R1, c, "Refuses to delete without -f."))
	case "checkout", "restore", "switch":
		switch {
		case f.hasAny(".") || anyPrefix(f.pos, ":/"):
			return one(fnd(R3, c, "Discards every uncommitted change in the working folder. They cannot be brought back.").
				WithAlt("git stash", "saves your changes first so they can be brought back"))
		case f.hasAny("--") && len(f.pos) > 1: // git checkout -- file: throws away that file's edits
			return one(fnd(R2, c, "Discards the uncommitted changes in these files."))
		}
		return one(fnd(R1, c, "Switches branches or restores files."))
	case "commit":
		if f.hasAny("--amend") {
			return one(fnd(R2, c, "Rewrites the last commit. The old one stays in the reflog."))
		}
		return one(fnd(R1, c, "Records a commit."))
	case "branch":
		if f.hasAny("-D") || (f.has('d', "delete") && f.has('f', "force")) {
			return one(fnd(R2, c, "Deletes a branch even if it is not merged. The branch can be recovered from the reflog (git reflog) for a while."))
		}
		return one(fnd(R0, c, "Lists or creates branches."))
	case "filter-branch", "filter-repo":
		return one(fnd(R3, c, "Rewrites the whole commit history. Other copies of the repository no longer match.").
			WithAlt("git clone --mirror . ../backup.git", "keeps a full copy first"))
	case "rebase", "commit-tree":
		return one(fnd(R2, c, "Rewrites commit history."))
	case "gc", "reflog", "prune":
		if f.hasAny("--prune=now", "--expire=now", "--expire-unreachable=now") {
			return one(fnd(R3, c, "Permanently erases the safety net (reflog and unreachable commits) used to recover lost work."))
		}
		return one(fnd(R1, c, "Housekeeping."))
	case "stash":
		if len(f.pos) > 1 && (f.pos[1] == "drop" || f.pos[1] == "clear") {
			return one(fnd(R2, c, "Deletes saved stashes."))
		}
		return one(fnd(R1, c, "Saves or restores changes."))
	case "fetch", "pull":
		return one(fnd(R1, c, "Downloads new commits from the remote."))
	case "remote":
		if len(f.pos) <= 1 || f.pos[1] == "show" || f.pos[1] == "get-url" || f.pos[1] == "-v" {
			return one(fnd(R0, c, "Only lists remotes."))
		}
		return one(fnd(R1, c, "Changes the list of remotes."))
	case "config":
		if f.hasAny("--get", "--list", "-l", "--get-all") || len(f.pos) <= 2 {
			return one(fnd(R0, c, "Only reads git settings."))
		}
		return one(fnd(R1, c, "Changes a git setting."))
	case "status", "log", "diff", "show", "blame", "ls-files", "describe", "rev-parse", "grep", "shortlog", "ls-remote", "":
		return one(fnd(R0, c, "Only reads repository information."))
	}
	return one(fnd(R1, c, "Changes the local repository. Reversible through git history."))
}

func anyPrefix(ss []string, p string) bool {
	for _, s := range ss {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
