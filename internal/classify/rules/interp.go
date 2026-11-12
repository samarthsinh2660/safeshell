package rules

import (
	"regexp"

	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

var interpPrograms = map[string]bool{"python": true, "python3": true, "perl": true, "ruby": true, "node": true}

var (
	reCritDelete = regexp.MustCompile(`(?i)\b(rmtree|rm_rf|rm_r|rmSync|rmdirSync|removedirs|rmdir)\s*\(\s*[rRfF]?['"](/|/\*|~|~/|/home|/etc|/usr|/var|/boot|/bin|/lib|/root)['"]`)
	reTreeDelete = regexp.MustCompile(`(?i)\b(rmtree|rm_rf|rm_r|rmSync|rmdirSync|removedirs|remove_tree)\b`)
	reMassDelete = regexp.MustCompile(`(?i)\b(unlink|File\.delete|FileUtils\.rm|os\.remove|os\.unlink)\b[^;\n]*(\bglob\b|<[^>;\n]*\*|['"][^'";\n]*\*)`)
	reFileDelete = regexp.MustCompile(`(?i)\b(os\.remove|os\.unlink|unlinkSync|fs\.unlink|File\.delete|unlink)\b`)
	reFileWrite  = regexp.MustCompile(`(?i)open\([^)]*,\s*[rRbB]?['"][wa]|\b(writeFile\w*|write_text|write_bytes|File\.write|truncate)\b`)
	reSpawn      = regexp.MustCompile(`(?i)\b(os\.system|os\.popen|subprocess|child_process|popen|Popen|spawn\w*|exec\w*|system)\b`)
)

// rateInterp rates python, perl, ruby and node. Text given inline (-c / -e) is
// scanned for destructive calls; shell commands inside it are rated separately
// by the parser. This is a best-effort scan: such code can always hide intent,
// so anything not clearly harmless is R2 and gets tested in the sandbox.
// known is false for a script file, whose effects cannot be seen from here.
func rateInterp(c shellparse.Command) (fs []Finding, known bool) {
	code := shellparse.InlineCode(c)
	if code == "" {
		return one(fnd(R1, c, "Runs a program from a file. What it does depends on the file, so it will be tested in the sandbox.")), false
	}
	switch {
	case reCritDelete.MatchString(code):
		fs = append(fs, fnd(R4, c, "Inline code deletes a whole system or home directory."))
	case reTreeDelete.MatchString(code):
		fs = append(fs, fnd(R3, c, "Inline code recursively deletes a folder with no recycle bin."))
	case reMassDelete.MatchString(code):
		fs = append(fs, fnd(R3, c, "Inline code deletes many files at once with no recycle bin."))
	case reFileDelete.MatchString(code):
		fs = append(fs, fnd(R2, c, "Inline code deletes files."))
	case reFileWrite.MatchString(code):
		fs = append(fs, fnd(R2, c, "Inline code writes or overwrites files."))
	case reSpawn.MatchString(code):
		fs = append(fs, fnd(R2, c, "Inline code starts other programs; any literal command in it is rated separately."))
	default:
		fs = append(fs, fnd(R2, c, "Runs inline code whose effects cannot be known from its text. It will be tested in the sandbox."))
	}
	return fs, true
}
