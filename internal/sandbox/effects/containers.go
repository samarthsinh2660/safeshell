package effects

import (
	"errors"
	"fmt"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
)

var errNotReadOnly = errors.New("refused to run a command that is not on the read-only list")

// toolQuery runs only the read-only queries of docker or kubectl that its
// callers list, so a mistake in this package can never change a machine or a
// cluster. globals are the options that choose the daemon or cluster (such as
// --context), repeated on every query so the answer comes from the same place
// the command would act on.
type toolQuery struct {
	run     Runner
	tool    string
	globals []string
}

// readOnlyQueries are the first words of the queries each tool may be asked.
var readOnlyQueries = map[string][][]string{
	"docker":  {{"ps"}, {"images"}, {"container", "inspect"}, {"volume", "ls"}, {"volume", "inspect"}, {"network", "ls"}, {"system", "df"}},
	"kubectl": {{"get"}},
}

// out runs one allowed query. Output is returned even when the tool also
// reports an error (some names may not exist); only an error with no output at
// all is an error, since then nothing can be said.
func (q toolQuery) out(args ...string) ([]string, error) {
	allowed := false
	for _, w := range readOnlyQueries[q.tool] {
		allowed = allowed || (len(args) >= len(w) && strings.Join(args[:len(w)], " ") == strings.Join(w, " "))
	}
	if !allowed {
		return nil, errNotReadOnly
	}
	text, err := q.run(q.tool, append(append([]string{}, q.globals...), args...)...)
	if err != nil && strings.TrimSpace(text) == "" {
		return nil, err
	}
	return splitLines(text), nil
}

// objects is one kind of thing a command would remove.
type objects struct {
	noun  string // "container", "volume"...
	items []string
}

// removal builds the effect for what would be removed. Anything found raises
// the level to at least R2, and to R3 when keepsData is true (volumes,
// namespaces and persistent volumes hold data). Routine removals stay at their
// rule level however many there are. atMost says the list can only be an upper
// bound.
func removal(what string, groups []objects, keepsData, atMost bool) domain.Effect {
	e := domain.Effect{Kind: domain.EffectContainers, Evidence: domain.Inspected, MinLevel: domain.R0}
	var parts []string
	total := 0
	for _, g := range groups {
		if len(g.items) == 0 {
			continue
		}
		total += len(g.items)
		parts = append(parts, count(len(g.items), g.noun))
		e.Details = append(e.Details, listed(g.noun+" ", g.items)...)
	}
	if total == 0 {
		e.Summary = what + ": nothing would be removed"
		return e
	}
	e.MinLevel = domain.R2
	if keepsData {
		e.MinLevel = domain.R3
	}
	upTo := ""
	if atMost {
		upTo = "up to "
	}
	e.Summary = fmt.Sprintf("%s would remove %s%s", what, upTo, strings.Join(parts, ", "))
	return e
}

// count writes n of a noun in the right number: "1 volume", "2 volumes", "2 objects inside them".
func count(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	first, rest, _ := strings.Cut(noun, " ")
	if rest != "" {
		rest = " " + rest
	}
	return fmt.Sprintf("%d %ss%s", n, first, rest)
}

// unavailable is the note shown when the tool, its daemon or the cluster did
// not answer. It is ESTIMATED and can only keep the rule level.
func unavailable(tool string) domain.Effect {
	return domain.Effect{Kind: domain.EffectContainers, Evidence: domain.Estimated, MinLevel: domain.R0,
		Summary: tool + " did not answer (it may not be installed or reachable, or nothing matches), so what would be removed is unknown and the rule level stands"}
}
