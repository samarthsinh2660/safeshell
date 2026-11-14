package effects

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// dbLargeCount is the number of rows or keys from which a change is rated R3.
const dbLargeCount = 1000

// sqlChangeRe finds the statements worth counting rows for.
var sqlChangeRe = regexp.MustCompile(`(?i)\b(delete\s+from|update\s+\w+\s+set|drop\s+table|truncate)\b`)

// InspectDatabases counts what a row-changing SQL statement or a redis delete
// would touch. It never sends the user's statement (the database may still run
// its own functions, views or row-level rules inside the count, as it would for
// the command itself): it builds a plain
// `SELECT count(*)` (or EXISTS, DBSIZE, INFO keyspace for redis) from a
// strictly parsed simple statement and runs it in read-only mode, with the
// connection options the command was given. A statement that is not simple, a
// password on the command line, a client option it does not know, a refused
// connection or a failed count give an ESTIMATED note and the rule level
// stands, never a count of zero.
func InspectDatabases(run Runner, cmds []shellparse.Command) []domain.Effect {
	var out []domain.Effect
	for _, c := range cmds {
		if c.Remote != "" || !changesData(c) {
			continue
		}
		e, ok := countRows(run, c)
		if !ok {
			e = couldNotCheck(c.Name)
		}
		out = append(out, e)
	}
	return out
}

// changesData is true when the command looks like it deletes or rewrites data.
func changesData(c shellparse.Command) bool {
	switch c.Name {
	case "psql", "mysql", "sqlite3":
		return sqlChangeRe.MatchString(strings.Join(c.Args, " ") + " " + c.Stdin)
	case "redis-cli":
		for _, a := range c.Args {
			switch strings.ToUpper(a) {
			case "DEL", "UNLINK", "FLUSHDB", "FLUSHALL":
				return true
			}
		}
	}
	return false
}

func couldNotCheck(tool string) domain.Effect {
	return domain.Effect{Kind: domain.EffectDatabase, Evidence: domain.Estimated, MinLevel: domain.R0,
		Summary: tool + ": could not check how much this would change (it is not a simple single statement, the database did not answer, or a password is on the command line), so the rule level stands"}
}

// countRows reads the command and asks for the count. ok is false when it
// cannot be done safely.
func countRows(run Runner, c shellparse.Command) (domain.Effect, bool) {
	if c.Sudo || c.Stdin != "" { // another user's connection, or statements this cannot see
		return domain.Effect{}, false
	}
	pass, sql, pos, ok := dbClients[c.Name].parse(c.Args)
	if !ok {
		return domain.Effect{}, false
	}
	if c.Name == "redis-cli" {
		return countKeys(run, pass, pos)
	}
	if c.Name == "sqlite3" {
		if len(pos) != 2 || (c.Cwd != "" && !filepath.IsAbs(pos[0])) {
			return domain.Effect{}, false
		}
		sql = pos[1]
	}
	ch, ok := parseChange(sql)
	if !ok {
		return domain.Effect{}, false
	}
	q := ch.countSQL()
	var args []string
	switch c.Name {
	case "psql":
		args = append(append([]string{"-X", "-w", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1"}, pass...),
			"-c", "BEGIN READ ONLY", "-c", "SET LOCAL statement_timeout = '10s'", "-c", q, "-c", "ROLLBACK")
		args = append(args, pos...)
	case "mysql":
		// --init-command= clears one set in an option file, which would run before the read-only mode.
		args = append(append([]string{}, pass...), "--init-command=", "--batch", "--skip-column-names", "-e",
			"SET SESSION MAX_EXECUTION_TIME=10000; SET SESSION TRANSACTION READ ONLY; START TRANSACTION READ ONLY; "+q+"; ROLLBACK")
		args = append(args, pos...)
	default: // -init replaces ~/.sqliterc, whose dot-commands can run programs
		args = []string{"-init", "/dev/null", "-safe", "-readonly", pos[0], q}
	}
	text, err := run(c.Name, args...)
	n, ok := oneCount(text)
	if err != nil || !ok {
		return domain.Effect{}, false
	}
	return ch.effect(n, q), true
}

// oneCount reads output that is exactly one non-negative whole number.
func oneCount(text string) (int, bool) {
	lines := splitLines(text)
	if len(lines) != 1 {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(lines[0], "(integer) "))
	return n, err == nil && n >= 0
}

// clientOptions says which options of a database client are passed on, as
// typed. Anything else makes the preview give up, which is also how password
// options (-p, --password, -W, -a) and options that read other settings or
// write output (--defaults-file, -o, -f) are kept out.
type clientOptions struct {
	valShort string          // short options that take a value
	valLong  map[string]bool // long options that take a value, without dashes
	flags    map[string]bool // options that take none, with their dashes
	sqlShort byte            // the option that carries the statement, 0 for none
	sqlLong  string
	maxPos   int
}

var dbClients = map[string]clientOptions{
	"psql": {valShort: "hpUdc", valLong: set("host", "port", "username", "dbname", "command"), flags: set("-X", "-q", "-w", "--no-psqlrc", "--quiet", "--no-password"),
		sqlShort: 'c', sqlLong: "command", maxPos: 2},
	"mysql": {valShort: "hPuDSe", valLong: set("host", "port", "user", "database", "socket", "execute", "protocol"), flags: set("--no-defaults"),
		sqlShort: 'e', sqlLong: "execute", maxPos: 1},
	"sqlite3":   {maxPos: 2},
	"redis-cli": {valShort: "hpnu", valLong: set("user"), flags: set("--tls"), maxPos: 1 << 10},
}

func set(keys ...string) map[string]bool {
	m := map[string]bool{}
	for _, k := range keys {
		m[k] = true
	}
	return m
}

// secretRe finds credentials (a user, maybe with a password) in a connection string.
var secretRe = regexp.MustCompile(`://[^/\s]*@`)

// unsafeWord is a word with a password in it or one the shell may not have
// expanded yet, so what it names is unknown.
func unsafeWord(w string) bool {
	return strings.ContainsAny(w, "$`*?[") || secretRe.MatchString(w) || strings.Contains(strings.ToLower(w), "password")
}

// parse splits args into the options to pass on, the statement and the
// positional words (database names, a file, redis words). ok is false for an
// unknown option, a repeated or missing statement, a password, or a word the
// shell may not have expanded.
func (o clientOptions) parse(args []string) (pass []string, sql string, pos []string, ok bool) {
	hasSQL := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" { // what follows would reach the client as options
			return nil, "", nil, false
		}
		if !strings.HasPrefix(a, "-") {
			pos = append(pos, a)
			continue
		}
		if a == "-" { // standard input
			return nil, "", nil, false
		}
		if o.flags[a] {
			pass = append(pass, a)
			continue
		}
		var name, v string
		attached := false
		if strings.HasPrefix(a, "--") {
			name, v, attached = strings.Cut(a, "=")
			if !o.valLong[name[2:]] {
				return nil, "", nil, false
			}
		} else {
			name, v = a[:2], a[2:]
			attached = v != ""
			if !strings.Contains(o.valShort, a[1:2]) {
				return nil, "", nil, false
			}
		}
		if !attached {
			if i++; i >= len(args) {
				return nil, "", nil, false
			}
			v = args[i]
		}
		if name == "-"+string(rune(o.sqlShort)) || name == "--"+o.sqlLong {
			if hasSQL {
				return nil, "", nil, false
			}
			sql, hasSQL = v, true
			continue
		}
		if unsafeWord(v) {
			return nil, "", nil, false
		}
		pass = append(pass, name, v)
	}
	for _, p := range pos {
		if unsafeWord(p) {
			return nil, "", nil, false
		}
	}
	return pass, sql, pos, len(pos) <= o.maxPos && (hasSQL || o.sqlShort == 0)
}

// countKeys counts what redis-cli's DEL, UNLINK, FLUSHDB or FLUSHALL would
// remove, with EXISTS, DBSIZE or INFO keyspace, which only read.
func countKeys(run Runner, pass, words []string) (domain.Effect, bool) {
	if len(words) == 0 {
		return domain.Effect{}, false
	}
	cmd, rest := strings.ToUpper(words[0]), words[1:]
	var query []string
	var summary string // the text after "would delete", given the number of keys
	switch {
	case (cmd == "DEL" || cmd == "UNLINK") && len(rest) > 0:
		query = append([]string{"EXISTS"}, rest...)
		summary = " of the %d key(s) you named"
	case cmd == "FLUSHDB" && flushOptions(rest):
		query = []string{"DBSIZE"}
		summary = " in this database"
	case cmd == "FLUSHALL" && flushOptions(rest):
		query = []string{"INFO", "keyspace"}
		summary = " in every database"
	default:
		return domain.Effect{}, false
	}
	text, err := run("redis-cli", append(append([]string{}, pass...), query...)...)
	if err != nil {
		return domain.Effect{}, false
	}
	var n int
	var ok bool
	if cmd == "FLUSHALL" {
		n, ok = keyspaceKeys(text)
	} else {
		n, ok = oneCount(text)
	}
	if !ok {
		return domain.Effect{}, false
	}
	e := domain.Effect{Kind: domain.EffectDatabase, Evidence: domain.Inspected, MinLevel: domain.R0,
		Details: []string{"counted without changing anything, with: " + report.Printable(strings.Join(query, " "))}}
	keys := fmt.Sprintf("%s keys", report.Commas(n))
	if n == 1 {
		keys = "1 key"
	}
	if cmd == "DEL" || cmd == "UNLINK" {
		summary = fmt.Sprintf(summary, len(rest))
	}
	e.Summary = "would delete " + keys + summary
	if n > 0 {
		e.MinLevel = domain.R2
	}
	if n >= dbLargeCount {
		e.MinLevel = domain.R3
	}
	return e, true
}

func flushOptions(rest []string) bool {
	for _, r := range rest {
		if u := strings.ToUpper(r); u != "ASYNC" && u != "SYNC" {
			return false
		}
	}
	return true
}

// keyspaceRe reads the keys=N part of INFO keyspace lines such as "db0:keys=12,expires=0".
var keyspaceRe = regexp.MustCompile(`(?m)^db\d+:keys=(\d+)`)

// keyspaceKeys adds up the keys of every database in an INFO keyspace answer.
// An answer with no database lines means an empty server.
func keyspaceKeys(text string) (int, bool) {
	total := 0
	for _, m := range keyspaceRe.FindAllStringSubmatch(text, -1) {
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return 0, false
		}
		total += n
	}
	return total, strings.Contains(text, "# Keyspace")
}
