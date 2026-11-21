package undo

import (
	"fmt"
	"strings"
	"time"
)

// AptTx is one transaction of /var/log/apt/history.log.
type AptTx struct {
	Start       time.Time
	Commandline string   // as apt logs it: no sudo
	Installed   []string // pkg:arch, in the order logged, including automatic dependencies
	Automatic   int      // how many of Installed were pulled in as dependencies
	Upgraded    int
	Removed     int
}

// AptInstall says whether line is a plain apt or apt-get install, the only
// command whose transaction a plan can be matched to.
func AptInstall(line string) bool {
	w := words(line)
	if len(w) < 2 || (w[0] != "apt" && w[0] != "apt-get") {
		return false
	}
	for _, a := range w[1:] {
		if !strings.HasPrefix(a, "-") {
			return a == "install"
		}
	}
	return false
}

// ParseAptHistory reads the log text. Times are local, so loc is where it was written.
func ParseAptHistory(text string, loc *time.Location) []AptTx {
	var txs []AptTx
	for _, block := range strings.Split(text, "\n\n") {
		var tx AptTx
		for _, l := range strings.Split(block, "\n") {
			key, val, ok := strings.Cut(l, ": ")
			if !ok {
				continue
			}
			switch key {
			case "Start-Date":
				tx.Start, _ = time.ParseInLocation("2006-01-02  15:04:05", val, loc)
			case "Commandline":
				tx.Commandline = val
			case "Install":
				tx.Installed, tx.Automatic = packages(val)
			case "Upgrade":
				up, _ := packages(val)
				tx.Upgraded = len(up)
			case "Remove":
				rm, _ := packages(val)
				tx.Removed = len(rm)
			}
		}
		if !tx.Start.IsZero() {
			txs = append(txs, tx)
		}
	}
	return txs
}

// packages reads "a:amd64 (1.0), b:amd64 (2.0, automatic)": the names, and how
// many are automatic. Commas inside the brackets belong to the entry.
func packages(s string) (names []string, auto int) {
	depth, start := 0, 0
	add := func(e string) {
		e = strings.TrimSpace(e)
		if name, _, _ := strings.Cut(e, " ("); name != "" {
			names = append(names, name)
			if strings.Contains(e, ", automatic") {
				auto++
			}
		}
	}
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				add(s[start:i])
				start = i + 1
			}
		}
	}
	add(s[start:])
	return names, auto
}

// aptWindow is how long after the guard approved a command apt may start (a
// sudo password prompt can take a while).
const aptWindow = 30 * time.Minute

// AptRemovePlan finds the transaction that line started, between created and
// created+30 minutes, and proposes removing exactly what it installed. ok is
// false when there is no such transaction or it installed nothing.
func AptRemovePlan(txs []AptTx, line string, created time.Time) (Plan, bool) {
	want := strings.Join(words(line), " ")
	from := created.Truncate(time.Second) // the log has whole seconds
	for _, tx := range txs {
		if tx.Start.Before(from) || tx.Start.After(created.Add(aptWindow)) || want != strings.Join(strings.Fields(tx.Commandline), " ") {
			continue
		}
		if len(tx.Installed) == 0 {
			return Plan{}, false
		}
		for _, p := range tx.Installed {
			if !pkgName.MatchString(p) {
				return Plan{}, false
			}
		}
		return Plan{Command: "sudo apt remove " + strings.Join(tx.Installed, " "), Explain: aptExplain(tx)}, true
	}
	return Plan{}, false
}

func aptExplain(tx AptTx) string {
	s := fmt.Sprintf("Removes the %d packages that command installed", len(tx.Installed))
	if tx.Automatic > 0 {
		s += fmt.Sprintf(" (%d of them only as dependencies)", tx.Automatic)
	}
	s += ". Their settings files stay."
	if tx.Upgraded > 0 || tx.Removed > 0 {
		s += fmt.Sprintf(" The same run also upgraded %d and removed %d packages; those are not reversed.", tx.Upgraded, tx.Removed)
	}
	return s
}
