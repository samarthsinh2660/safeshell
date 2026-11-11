// Package classify rates a command line by how reversible its effects are.
// It is pure: no file, process or network access.
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
	fs = append(fs, redirectFindings(c)...)
	if c.Sudo {
		for i := range fs {
			if fs[i].Level >= R1 {
				fs[i].Level = min(R4, fs[i].Level+1)
				fs[i].Reason += " It runs as root, so system files are not protected."
			}
		}
	}
	if c.Remote != "" {
		for i := range fs {
			fs[i].Reason = "On remote host " + c.Remote + ": " + fs[i].Reason
		}
	}
	return fs, unknown
}

var harmlessTargets = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/tty": true, "/dev/zero": true}

func redirectFindings(c shellparse.Command) []Finding {
	var fs []Finding
	for _, r := range c.Redirs {
		if r.Op != ">" && r.Op != ">>" && r.Op != ">|" && r.Op != "&>" && r.Op != "&>>" && r.Op != "<>" {
			continue
		}
		t := rules.Norm(r.Target)
		appending := strings.HasSuffix(r.Op, ">>")
		base := c.String()
		cmd := strings.TrimSpace(base + " " + r.Op + " " + r.Target)
		switch {
		case harmlessTargets[t] || strings.HasPrefix(t, "/dev/fd/"):
		case rules.IsBlockDevice(t) || t == "/dev/mem" || t == "/dev/kmem":
			fs = append(fs, finding(R4, cmd, "Writes raw data onto "+t+", destroying what is stored there."))
		case strings.HasPrefix(t, "/proc/") || strings.HasPrefix(t, "/sys/"):
			fs = append(fs, finding(R4, cmd, "Writes to a live kernel control file ("+t+"), which can crash or reboot the machine."))
		case rules.UnderSystem(t) && !appending:
			fs = append(fs, finding(R3, cmd, "Overwrites the system file "+t+".").
				WithAlt("cp "+rules.Shq(t)+" "+rules.Shq(t)+".bak", "make a backup of the file first"))
		case rules.UnderSystem(t):
			fs = append(fs, finding(R2, cmd, "Appends to the system file "+t+"."))
		case appending:
			fs = append(fs, finding(R1, cmd, "Appends to "+t+"."))
		default:
			fs = append(fs, finding(R2, cmd, "Overwrites "+t+" if it already exists.").
				WithAlt(strings.TrimSpace(base+" >> "+r.Target), "appends instead of overwriting"))
		}
	}
	return fs
}

func finding(l Level, cmd, reason string) Finding {
	return Finding{Level: l, Command: cmd, Reason: reason}
}

var fetchers = map[string]bool{"curl": true, "wget": true, "fetch": true, "base64": true, "xxd": true, "openssl": true, "nc": true, "ncat": true}

// pipelineFindings looks for dangerous patterns that only exist across a pipe,
// such as downloading code and running it unseen.
func pipelineFindings(p shellparse.Pipeline) []Finding {
	var fs []Finding
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
