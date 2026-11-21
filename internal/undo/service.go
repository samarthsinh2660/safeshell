package undo

import (
	"fmt"
	"strings"
)

// UnitState is what systemctl said about a unit before a command changed it.
type UnitState struct {
	Active  string // is-active: active, inactive, failed, ...
	Enabled string // is-enabled: enabled, disabled, masked, static, ...
}

// ServiceCmd is a systemctl stop, disable or mask.
type ServiceCmd struct {
	Verb  string
	Now   bool // --now: also stops the unit
	Units []string
}

// ParseServiceCmd reads a plain systemctl stop, disable or mask with unit
// names. Any other option makes it unreadable: ok is false, and no plan is
// offered for what cannot be named exactly.
func ParseServiceCmd(line string) (c ServiceCmd, ok bool) {
	w := words(line)
	if len(w) < 3 || w[0] != "systemctl" {
		return c, false
	}
	switch w[1] {
	case "stop", "disable", "mask":
		c.Verb = w[1]
	default:
		return c, false
	}
	for _, a := range w[2:] {
		switch {
		case a == "--now":
			c.Now = true
		case strings.HasPrefix(a, "-") || !unitName.MatchString(a):
			return ServiceCmd{}, false
		default:
			c.Units = append(c.Units, a)
		}
	}
	return c, len(c.Units) > 0
}

// Restore is the plan that puts unit back as st said it was. ok is false when
// the command changed nothing that needs restoring.
func (c ServiceCmd) Restore(unit string, st UnitState) (Plan, bool) {
	var cmds, did []string
	add := func(verb, what string) {
		cmds = append(cmds, "sudo systemctl "+verb+" "+unit)
		did = append(did, what)
	}
	if c.Verb == "mask" && !strings.HasPrefix(st.Enabled, "masked") {
		add("unmask", "unmasks it")
	}
	if c.Verb == "disable" && strings.HasPrefix(st.Enabled, "enabled") {
		add("enable", "enables it again")
	}
	if (c.Verb == "stop" || c.Now) && st.Active == "active" {
		add("start", "starts it again")
	}
	if len(cmds) == 0 {
		return Plan{}, false
	}
	return Plan{
		Command: strings.Join(cmds, " && "),
		Explain: fmt.Sprintf("%s was %s and %s before; this %s.", unit, st.Active, st.Enabled, strings.Join(did, " and ")),
	}, true
}
