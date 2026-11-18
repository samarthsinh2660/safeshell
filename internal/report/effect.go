package report

import (
	"fmt"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/sandbox"
)

// FileEffect turns an observed file diff into an Effect: what happened, the
// biggest changes, and the level its impact justifies.
func FileEffect(d sandbox.FileDiff) domain.Effect {
	t := d.Totals()
	im := Score(d)
	e := domain.Effect{Kind: domain.EffectFS, Evidence: domain.Observed, MinLevel: im.Level}
	var parts []string
	if t.Deleted > 0 {
		parts = append(parts, fmt.Sprintf("delete %s (%s)", Files(t.Deleted), HumanBytes(t.DeletedBytes)))
	}
	if t.Modified > 0 {
		parts = append(parts, "change "+Files(t.Modified))
	}
	if t.Created > 0 {
		parts = append(parts, "create "+Files(t.Created))
	}
	if len(parts) == 0 {
		e.Summary = "changes no files"
	} else {
		e.Summary = "would " + strings.Join(parts, ", ")
	}
	if d.Truncated {
		e.Summary += " (counting stopped at a limit; the real total is larger)"
	}
	for _, r := range im.Reasons {
		e.Details = append(e.Details, "because it "+r)
	}
	const max = 8
	for i, c := range d.Changes {
		if i == max {
			e.Details = append(e.Details, fmt.Sprintf("... and %d more changes", len(d.Changes)-max))
			break
		}
		slash := ""
		if c.IsDir {
			slash = "/"
		}
		line := fmt.Sprintf("%s /%s%s", c.Kind, c.Path, slash)
		if c.IsDir && c.Files > 0 {
			line += " (" + Files(c.Files) + ")"
		}
		e.Details = append(e.Details, line)
	}
	return e
}

// RootEffect describes a command that tried to run something as root (sudo, su,
// ...). The sandbox cannot do that, so what it would do is not known: the effect
// is an estimate at R3, which keeps the command from running unattended.
func RootEffect(asks []string) (e domain.Effect, ok bool) {
	if len(asks) == 0 {
		return e, false
	}
	return domain.Effect{Kind: domain.EffectFS, Evidence: domain.Estimated, MinLevel: domain.R3,
		Summary: "This script asks for root, which the preview cannot show. What it would do as root is not known.",
		Details: []string{"tried to run " + strings.Join(asks, ", ")}}, true
}

// ignoredSockets are lookups every libc program makes; they say nothing about the command.
var ignoredSockets = map[string]bool{"/var/run/nscd/socket": true, "/run/nscd/socket": true}

// NetEffect describes the network operations the command tried inside the
// sandbox, where they all failed. ok is false when there were none worth showing.
func NetEffect(attempts []sandbox.NetAttempt) (e domain.Effect, ok bool) {
	var shown []sandbox.NetAttempt
	for _, a := range attempts {
		if !ignoredSockets[a.Addr] {
			shown = append(shown, a)
		}
	}
	if len(shown) == 0 {
		return e, false
	}
	e = domain.Effect{Kind: domain.EffectNetwork, Evidence: domain.Observed, MinLevel: domain.R2,
		Summary: "would reach out over the network or to a local service; blocked in the sandbox, but it would go through for real"}
	for _, a := range shown {
		e.Details = append(e.Details, a.String())
	}
	return e, true
}
