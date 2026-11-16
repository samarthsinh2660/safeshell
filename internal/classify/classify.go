package classify

import (
	"slices"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/classify/rules"
	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Local aliases keep this file readable.
type (
	Finding     = domain.Finding
	Verdict     = domain.Verdict
	Alternative = domain.Alternative
	Level       = domain.Level
)

const (
	R0 = domain.R0
	R1 = domain.R1
	R2 = domain.R2
	R3 = domain.R3
	R4 = domain.R4
)

// Classify rates a whole command line. The result is the highest level of any
// command in it; every reason and safer alternative is kept for the report.
func Classify(line string) Verdict {
	if strings.TrimSpace(line) == "" {
		return Verdict{Level: R0}
	}
	res, err := shellparse.Parse(line)
	if err != nil {
		return Verdict{Level: R2, Findings: []Finding{{
			Level:   R2,
			Command: line,
			Reason:  "Could not be understood as a shell command, so it cannot be checked. Review it by eye.",
		}}}
	}
	var fs []Finding
	var unclassified []string
	if res.ForkBomb {
		fs = append(fs, Finding{Level: R4, Command: line, Reason: "A fork bomb: it starts copies of itself until the machine runs out of resources and freezes."})
	}
	for _, p := range res.Pipelines {
		fs = append(fs, pipelineFindings(p)...)
		for _, c := range p.Cmds {
			cf, unknown := commandFindings(c)
			fs = append(fs, cf...)
			if unknown {
				unclassified = append(unclassified, c.Name)
			}
		}
	}
	v := Verdict{Findings: fs, Unclassified: dedupe(unclassified)}
	v.Level = domain.MaxLevel(fs)
	seen := map[string]bool{}
	for _, f := range fs {
		if f.Alt != nil && f.Level >= R2 && !seen[f.Alt.Command] {
			seen[f.Alt.Command] = true
			v.Alternatives = append(v.Alternatives, *f.Alt)
		}
	}
	return v
}

func dedupe(ss []string) []string {
	slices.Sort(ss)
	return slices.Compact(ss)
}

// commandFindings applies the rule for one command, then adds redirect and
// privilege effects. The bool is true when the command is not in the rule table.
func commandFindings(c shellparse.Command) ([]Finding, bool) {
	var fs []Finding
	var known, unknown bool
	if c.Name != "" {
		fs, known = rules.Rate(c)
		unknown = !known
	}
	if c.AliasWord {
		fs = slices.DeleteFunc(fs, func(f Finding) bool { return f.Level < R3 })
		unknown = false
	}
	if c.Sudo {
		for i := range fs {
			if fs[i].Level >= R1 {
				fs[i].Level = min(R4, fs[i].Level+1)
				fs[i].Reason += " It runs as root, so system files are not protected."
			}
		}
	}
	// The user's shell opens redirects, so sudo does not raise them.
	fs = append(fs, redirectFindings(c)...)
	if c.Remote != "" {
		for i := range fs {
			fs[i].Reason = "On remote host " + c.Remote + ": " + fs[i].Reason
		}
	}
	return fs, unknown
}

func redirectFindings(c shellparse.Command) []Finding {
	var fs []Finding
	for _, r := range c.Redirs {
		if r.Op != ">" && r.Op != ">>" && r.Op != ">|" && r.Op != "&>" && r.Op != "&>>" && r.Op != "<>" {
			continue
		}
		cmd := strings.TrimSpace(c.String() + " " + r.Op + " " + r.Target)
		alt := strings.TrimSpace(c.String() + " >> " + r.Target)
		if f, ok := rules.WriteTarget(cmd, r.Target, strings.HasSuffix(r.Op, ">>"), alt); ok {
			fs = append(fs, f)
		}
	}
	return fs
}

func finding(l Level, cmd, reason string) Finding {
	return Finding{Level: l, Command: cmd, Reason: reason}
}

var fetchers = map[string]bool{"curl": true, "wget": true, "fetch": true, "base64": true, "xxd": true, "openssl": true, "nc": true, "ncat": true}

// pipelineFindings looks for dangerous patterns that only exist across a pipe,
// such as downloading code and running it unseen, or sending a secret file out.
func pipelineFindings(p shellparse.Pipeline) []Finding {
	fs := rules.SecretSends(p)
	for i, c := range p.Cmds {
		fromNet := rules.RunsFetchedCode(c)
		if rules.IsInterpreter(c.Name) && rules.ReadsStdin(c) {
			fromNet = fromNet || c.Downloads || (i > 0 && fetchers[p.Cmds[i-1].Name])
		}
		if !fromNet {
			continue
		}
		lvl := R3
		if c.Sudo {
			lvl = R4
		}
		fs = append(fs, finding(lvl, c.String(), "Downloads code and runs it immediately without you reading it. Whoever controls that server controls your machine.").
			WithAlt("curl -fsSL URL -o script.sh && less script.sh && bash script.sh", "download first, read it, then run it"))
	}
	return fs
}
