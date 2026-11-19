package classify

import (
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// LineFinding is the rating of one line of a script.
type LineFinding struct {
	Line    int      // 1-based; where the command starts when it spans several lines
	Text    string   // that line of the script, as written
	Level   Level    // the highest level of any command that starts on the line
	Reasons []string // why, in plain English; empty for a line rated R0
}

// Script rates every command in a script and says on which line it is. It
// returns one entry for each line that starts a command, in line order; lines
// with only comments, blank space or the inside of a here-document have none.
// It is an error when the script is not valid shell.
func Script(src, home string) ([]LineFinding, error) {
	res, err := shellparse.ParseScript(src)
	if err != nil {
		return nil, err
	}
	text := strings.Split(src, "\n")
	byLine := map[int][]Finding{}
	add := func(line int, fs ...Finding) { byLine[line] = append(byLine[line], fs...) }
	for _, p := range res.Pipelines {
		fs, _ := rateStatement(p, home)
		add(p.Line, fs...)
	}
	if res.ForkBomb {
		add(res.ForkBombLine, Finding{Level: R4, Command: text[res.ForkBombLine-1], Reason: forkBombReason})
	}
	out := make([]LineFinding, 0, len(byLine))
	for line, fs := range byLine {
		lf := LineFinding{Line: line, Text: strings.TrimSpace(text[line-1]), Level: domain.MaxLevel(fs)}
		for _, f := range fs {
			if f.Level > R0 {
				lf.Reasons = append(lf.Reasons, f.Reason)
			}
		}
		out = append(out, lf)
	}
	slices.SortFunc(out, func(a, b LineFinding) int { return a.Line - b.Line })
	return out, nil
}

// ScriptLevel is the level of a whole script: the highest of its lines.
func ScriptLevel(lines []LineFinding) Level {
	l := R0
	for _, lf := range lines {
		l = max(l, lf.Level)
	}
	return l
}
