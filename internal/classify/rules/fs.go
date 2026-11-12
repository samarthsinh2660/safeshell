package rules

import (
	"regexp"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

func ruleRm(c shellparse.Command, f flags) []Finding {
	recursive := f.has('r', "recursive") || f.has('R', "")
	var out []Finding
	if f.hasAny("--no-preserve-root") {
		out = append(out, fnd(R4, c, "Turns off the safety check that stops `rm` from deleting the whole filesystem."))
	}
	for _, p := range f.pos {
		ok, what := Critical(p)
		switch {
		case ok && recursive:
			out = append(out, fnd(R4, c, "Recursively deletes "+what+". This would destroy the system or all your data."))
		case ok:
			out = append(out, fnd(R3, c, "Deletes "+what+" permanently."))
		}
	}
	if domain.MaxLevel(out) >= R3 {
		return out
	}
	trash := "trash-put " + targets(f)
	switch {
	case recursive && anyWholeCwd(f.pos):
		return one(fnd(R3, c, "Recursively deletes everything in the current directory. Deleted files cannot be recovered.").
			WithAlt(trash, "moves them to the Trash so they can be restored"))
	case recursive:
		return one(fnd(R3, c, "Recursively and permanently deletes whole folders. There is no recycle bin.").
			WithAlt(trash, "moves them to the Trash so they can be restored"))
	case c.ViaXargs || c.ViaFind:
		return one(fnd(R3, c, "Deletes whatever files are passed in, and you cannot see the list first.").
			WithAlt("trash-put <the same files>", "moves them to the Trash so they can be restored"))
	case len(f.pos) == 0:
		return one(fnd(R1, c, "No files given, so nothing will be deleted."))
	}
	return one(fnd(R2, c, "Permanently deletes files. There is no recycle bin.").WithAlt(trash, "moves them to the Trash so they can be restored"))
}

func anyWholeCwd(ps []string) bool {
	for _, p := range ps {
		if wholeCwd(p) {
			return true
		}
	}
	return false
}

func targets(f flags) string {
	q := make([]string, len(f.pos))
	for i, p := range f.pos {
		q[i] = Shq(p)
	}
	return strings.Join(q, " ")
}

func ruleShred(c shellparse.Command, f flags) []Finding {
	for _, p := range f.pos {
		if IsBlockDevice(p) {
			return one(fnd(R4, c, "Overwrites a whole disk with random data. Everything on it is destroyed."))
		}
	}
	return one(fnd(R3, c, "Overwrites files so they can never be recovered, even with recovery tools."))
}

func ruleMv(c shellparse.Command, f flags) []Finding {
	var out []Finding
	for i, p := range f.pos {
		if ok, what := Critical(p); ok && i < len(f.pos)-1 {
			out = append(out, fnd(R4, c, "Moves "+what+" away, which breaks the system or loses access to your files."))
		}
	}
	if n := len(f.pos); n > 1 && f.pos[n-1] == "/dev/null" {
		out = append(out, fnd(R3, c, "Moving files to /dev/null destroys them."))
	}
	if len(out) > 0 {
		return out
	}
	if f.has('f', "force") {
		return one(fnd(R2, c, "Overwrites existing files at the destination without asking.").WithAlt("mv -i "+targets(f), "asks before overwriting"))
	}
	return one(fnd(R1, c, "Moves files. Undo is moving them back, unless one overwrites an existing file."))
}

func ruleCp(c shellparse.Command, f flags) []Finding {
	if n := len(f.pos); n > 1 && UnderSystem(Norm(f.pos[n-1])) {
		return one(fnd(R2, c, "Overwrites a system file with this one. A wrong file here can stop a service from starting."))
	}
	if len(f.pos) > 1 && f.pos[0] == "/dev/null" {
		return one(fnd(R2, c, "Empties the target file."))
	}
	if f.has('f', "force") {
		return one(fnd(R2, c, "Overwrites existing files at the destination without asking.").WithAlt("cp -i "+targets(f), "asks before overwriting"))
	}
	return one(fnd(R1, c, "Copies files. An existing file at the destination is overwritten."))
}

func ruleLn(c shellparse.Command, f flags) []Finding {
	if f.has('f', "force") {
		return one(fnd(R2, c, "Replaces an existing file or link."))
	}
	return one(fnd(R1, c, "Creates a link."))
}

func ruleSed(c shellparse.Command, f flags) []Finding {
	if f.has('i', "in-place") {
		return one(fnd(R2, c, "Edits files in place. The original text is not kept.").WithAlt(strings.Replace(c.String(), "sed -i", "sed -i.bak", 1), "keeps a .bak copy of each file"))
	}
	return one(fnd(R0, c, "Only prints the edited text; files are not changed."))
}

func ruleTee(c shellparse.Command, f flags) []Finding {
	if f.has('a', "append") {
		return one(fnd(R1, c, "Appends to files."))
	}
	return one(fnd(R2, c, "Overwrites the files it is given."))
}

func ruleFind(c shellparse.Command, f flags) []Finding {
	if f.hasAny("-delete") {
		for _, p := range f.pos {
			if ok, what := Critical(p); ok {
				return one(fnd(R4, c, "Deletes every match under "+what+"."))
			}
		}
		return one(fnd(R3, c, "Deletes every file that matches. You cannot see the list first.").
			WithAlt(strings.Replace(c.String(), "-delete", "-print", 1), "only lists what would be deleted"))
	}
	return one(fnd(R0, c, "Only searches. Any -exec command is checked separately."))
}

func ruleTar(c shellparse.Command, f flags) []Finding {
	if f.has('t', "list") && !f.has('x', "extract") && !f.has('c', "create") {
		return one(fnd(R0, c, "Only lists what is inside the archive."))
	}
	if f.has('x', "extract") && !f.hasAny("--remove-files") {
		return one(fnd(R2, c, "Extracts files, overwriting any that already exist with the same name."))
	}
	if f.hasAny("--remove-files") {
		return one(fnd(R2, c, "Deletes the original files after adding them to the archive."))
	}
	return one(fnd(R1, c, "Creates or extracts an archive. Extracting can overwrite files."))
}

func ruleUnzip(c shellparse.Command, f flags) []Finding {
	if f.has('l', "") || f.has('t', "") || f.has('Z', "") {
		return one(fnd(R0, c, "Only lists or tests the archive."))
	}
	if f.has('o', "") {
		return one(fnd(R2, c, "Extracts and overwrites existing files without asking."))
	}
	return one(fnd(R1, c, "Extracts an archive."))
}

var awkWrites = regexp.MustCompile(`(print|printf)[^;}|]*>>?\s*["/$a-zA-Z]|system\s*\(|\|\s*getline|\|&|"\s*\|\s*(print|printf)|print[^;}]*\|\s*"`)

// ruleAwk: awk reads and prints, but its program can also write files and run commands.
func ruleAwk(c shellparse.Command, f flags) []Finding {
	prog := strings.Join(c.Args, " ")
	switch {
	case f.has('i', "inplace") || strings.Contains(prog, "-i inplace") || strings.Contains(prog, "-i  inplace"):
		return one(fnd(R2, c, "Edits files in place. The original text is not kept."))
	case awkWrites.MatchString(prog):
		return one(fnd(R2, c, "The awk program writes to files or runs other commands."))
	}
	return one(fnd(R0, c, "Only reads and prints text."))
}

func ruleTruncate(c shellparse.Command, f flags) []Finding {
	for _, p := range f.pos {
		if n := Norm(p); UnderSystem(n) || strings.HasPrefix(n, "/var/lib/") {
			return one(fnd(R3, c, "Cuts a system or database file. Its contents are lost and the service using it can fail."))
		}
	}
	return one(fnd(R2, c, "Cuts a file to a fixed size and can remove its contents."))
}

func ruleGpg(c shellparse.Command, f flags) []Finding {
	switch {
	case f.hasAny("--delete-secret-keys", "--delete-secret-and-public-key"):
		fd := fnd(R3, c, "Deletes a private key. Without a backup it cannot be recreated.")
		if id := gpgKeyID(f.all); id != "" {
			fd = fd.WithAlt("gpg --export-secret-keys -a "+id+" > safeshell-gpg-backup-"+id+".asc", "saves a backup of the private key first")
		}
		return one(fd)
	case f.hasAny("--delete-keys", "--delete-key"):
		return one(fnd(R2, c, "Deletes a public key from your keyring. It can be imported again."))
	}
	return one(fnd(R1, c, "Runs gpg."))
}

// gpgKeyID returns the first key id operand, skipping options and their values.
func gpgKeyID(args []string) string {
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--homedir" || a == "--keyring" || a == "--keyserver" || a == "-u" || a == "--local-user":
			i++
		case !strings.HasPrefix(a, "-"):
			return a
		}
	}
	return ""
}
