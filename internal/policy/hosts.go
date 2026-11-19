package policy

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Host labels a machine ssh connects to. Only Prod changes anything.
const (
	Prod    = "prod"
	Staging = "staging"
)

// ValidateHosts checks the user's host patterns and labels, naming the first
// bad entry.
func ValidateHosts(hosts map[string]string) error {
	for _, pat := range sortedKeys(hosts) {
		if _, err := path.Match(pat, ""); err != nil {
			return fmt.Errorf("hosts: %q is not a valid pattern. Use * to match any text, such as \"db-*.example.com\"", pat)
		}
		if pat == "" {
			return fmt.Errorf("hosts: a pattern is empty. Give a host name or pattern such as \"prod*\"")
		}
		if l := hosts[pat]; l != Prod && l != Staging {
			return fmt.Errorf("hosts: the label for %q must be %q or %q, not %q", pat, Prod, Staging, l)
		}
	}
	return nil
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// HostLabel is the label of host (as typed to ssh, so user@ is ignored and case
// does not matter), or "" when no pattern matches. When patterns of both labels
// match, prod wins: the safer reading.
func HostLabel(hosts map[string]string, host string) string {
	if i := strings.LastIndex(host, "@"); i >= 0 {
		host = host[i+1:]
	}
	host = strings.ToLower(host)
	label := ""
	for pat, l := range hosts {
		if ok, _ := path.Match(strings.ToLower(pat), host); ok && (label == "" || l == Prod) {
			label = l
		}
	}
	return label
}

// HostOutcome is what the host labels ask of one command line. The zero value
// asks nothing.
type HostOutcome struct {
	MinLevel domain.Level `json:"min_level"`
	Reason   string       `json:"reason,omitempty"` // why the level was raised
	Note     string       `json:"note,omitempty"`   // an interactive session on a labelled host
}

// EvaluateHosts raises by one level (at most R4) any remote command that
// changes something (R2 or more) on a prod host, and notes an interactive
// session on any labelled host. Reads and unlabelled hosts are untouched.
func EvaluateHosts(hosts map[string]string, v domain.Verdict, cmds []shellparse.Command) HostOutcome {
	var o HostOutcome
	for _, f := range v.Findings {
		if f.Host == "" || f.Level < domain.R2 || HostLabel(hosts, f.Host) != Prod {
			continue
		}
		if l := min(domain.R4, f.Level+1); l > o.MinLevel {
			o.MinLevel = l
			o.Reason = fmt.Sprintf("this is a production host (%s)", f.Host)
		}
	}
	for _, c := range cmds {
		if path.Base(c.Name) != "ssh" || c.Remote != "" {
			continue
		}
		if host, remote := shellparse.SSHSplit(c.Args); host != "" && remote == "" && HostLabel(hosts, host) != "" {
			o.Note = fmt.Sprintf("%s is a labelled host. Commands you type there are only checked if SafeShell is installed on it.", host)
			break
		}
	}
	return o
}

// Apply returns the level and decision after the host labels. It never lowers either.
func (o HostOutcome) Apply(level domain.Level, d domain.Decision) (domain.Level, domain.Decision) {
	level = max(level, o.MinLevel)
	return level, max(d, Decide(level, nil))
}
