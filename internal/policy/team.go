package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// Rule is one line of the admin's team policy. A policy can only make things
// stricter: raise a level, block, or ask for a snapshot. It has no way to
// allow anything or lower a level.
type Rule struct {
	Match    []string     // a command name, then the words that must follow it
	Block    string       // when set: refuse, saying this
	Snapshot bool         // save the files it will change before it runs
	MinLevel domain.Level // R0 means no minimum
}

// Outcome is what the policy asks of one command line. The zero value asks nothing.
type Outcome struct {
	MinLevel domain.Level `json:"min_level"`
	Block    string       `json:"block,omitempty"` // the policy's reason; the first matching rule's
	Snapshot bool         `json:"snapshot,omitempty"`
}

// wire is a rule as written in the file.
type wire struct {
	Match    string  `json:"match"`
	Block    *string `json:"block"`
	Snapshot bool    `json:"snapshot"`
	MinLevel string  `json:"min_level"`
}

// Parse reads a policy file: {"rules":[{"match":"apt purge","snapshot":true}, ...]}.
// It refuses anything it does not understand, so a rule that tries to loosen
// something (an unknown field such as "allow") is an error, never ignored.
func Parse(data []byte) ([]Rule, error) {
	var f struct {
		Rules []wire `json:"rules"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%w (a policy can only make things stricter; the fields are rules, match, block, snapshot and min_level)", err)
	}
	if dec.More() {
		return nil, errors.New("there is text after the end of the policy")
	}
	var rules []Rule
	for i, w := range f.Rules {
		n := i + 1
		r := Rule{Match: strings.Fields(w.Match), Snapshot: w.Snapshot}
		if len(r.Match) == 0 {
			return nil, fmt.Errorf("rule %d: match is empty. Say which command it covers, such as \"docker system prune\"", n)
		}
		if w.Block != nil {
			if strings.TrimSpace(*w.Block) == "" {
				return nil, fmt.Errorf("rule %d: block needs a reason to show people", n)
			}
			r.Block = *w.Block
		}
		if w.MinLevel != "" {
			l, ok := domain.ParseLevel(w.MinLevel)
			if !ok || l == domain.R0 {
				return nil, fmt.Errorf("rule %d: min_level must be R1, R2, R3 or R4, not %q. A policy can raise a level, never lower one", n, w.MinLevel)
			}
			r.MinLevel = l
		}
		if r.Block == "" && !r.Snapshot && r.MinLevel == domain.R0 {
			return nil, fmt.Errorf("rule %d does nothing: add block, snapshot or min_level", n)
		}
		rules = append(rules, r)
	}
	return rules, nil
}

// Evaluate combines every rule that matches any command of the line.
func Evaluate(rules []Rule, cmds []shellparse.Command) Outcome {
	var o Outcome
	for _, r := range rules {
		for _, c := range cmds {
			if !r.matches(c) {
				continue
			}
			o.MinLevel = max(o.MinLevel, r.MinLevel)
			o.Snapshot = o.Snapshot || r.Snapshot
			if o.Block == "" {
				o.Block = r.Block
			}
		}
	}
	return o
}

// matches: the command's name (without its folder) and then its words that are
// not options, in order, start with the rule's words. sudo and other wrappers
// are already gone, so "apt purge" also matches "sudo apt -y purge x". An
// option that takes a value ("docker -H host system prune") hides the words
// after it; the rule is a guard rail, not a sandbox.
func (r Rule) matches(c shellparse.Command) bool {
	words := []string{path.Base(c.Name)}
	for _, a := range c.Args {
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) < len(r.Match) {
		return false
	}
	for i, w := range r.Match {
		if words[i] != w {
			return false
		}
	}
	return true
}

// Apply returns the level and decision after the policy: the level is raised to
// the policy's minimum, a block refuses, a snapshot rule makes sure the person
// is asked. It never lowers either.
func (o Outcome) Apply(level domain.Level, d domain.Decision) (domain.Level, domain.Decision) {
	level = max(level, o.MinLevel)
	d = max(d, Decide(level, nil))
	if o.Block != "" {
		d = max(d, domain.Block)
	}
	if o.Snapshot {
		d = max(d, domain.Confirm)
	}
	return level, d
}
