package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// Project is what a project's .safeshell.json asks of SafeShell. A cloned
// project is not trusted, so it can only tighten: protect folders inside the
// project, raise levels, block commands. Anything else is left out.
type Project struct {
	Protect []string // absolute folders inside the project root
	Rules   []Rule
}

// ParseProject reads a project file found in root. It uses the team policy's
// rules, plus a protect list of folders relative to root. Whatever would
// loosen or reach outside the project (an unknown key, a rule that lowers a
// level, a folder outside root) is dropped, and each drop is returned as a
// short warning naming the key. A file that is not valid JSON is dropped whole.
func ParseProject(data []byte, root string) (p Project, warnings []string) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return Project{}, []string{"it is not valid JSON, so all of it was ignored"}
	}
	for _, key := range slices.Sorted(maps.Keys(top)) {
		switch key {
		case "protect":
			var entries []string
			if err := json.Unmarshal(top[key], &entries); err != nil {
				warnings = append(warnings, `ignored "protect": it must be a list of folder names`)
				continue
			}
			for _, e := range entries {
				abs := path.Join(root, e)
				if e == "" || path.IsAbs(e) || !Under(root, abs) {
					warnings = append(warnings, fmt.Sprintf(`ignored "protect" entry %q: a project can only protect folders inside itself`, e))
					continue
				}
				p.Protect = append(p.Protect, abs)
			}
		case "rules":
			var raw []json.RawMessage
			if err := json.Unmarshal(top[key], &raw); err != nil {
				warnings = append(warnings, `ignored "rules": it must be a list`)
				continue
			}
			for i, r := range raw {
				rule, err := projectRule(r, i+1)
				if err != nil {
					warnings = append(warnings, fmt.Sprintf(`ignored "rules" entry %d: %v`, i+1, err))
					continue
				}
				p.Rules = append(p.Rules, rule)
			}
		default:
			warnings = append(warnings, fmt.Sprintf(`ignored %q: a project file can only protect folders and make rules stricter`, key))
		}
	}
	return p, warnings
}

// projectRule is one rule of a project file; a block reason says where it came from.
func projectRule(raw json.RawMessage, n int) (Rule, error) {
	var w wire
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&w); err != nil {
		return Rule{}, fmt.Errorf("%w (the fields are match, block, snapshot and min_level)", err)
	}
	r, err := w.rule(n)
	if err != nil {
		return Rule{}, err
	}
	if r.Block != "" {
		r.Block = "this project's .safeshell.json says: " + strings.TrimSpace(r.Block)
	}
	return r, nil
}
