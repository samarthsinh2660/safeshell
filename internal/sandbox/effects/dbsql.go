package effects

import (
	"strings"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/report"
)

// sqlChange is a statement as far as the preview reads it.
type sqlChange struct {
	verb  string // delete, update, drop, truncate
	table string
	where string // the condition, rebuilt from checked words; "" for none
}

func (c sqlChange) countSQL() string {
	q := "SELECT count(*) FROM " + c.table
	if c.where != "" {
		q += " WHERE " + c.where
	}
	return q
}

// sqlWord is one checked piece of a statement.
type sqlWord struct {
	kind byte // 'w' word, 's' string, 'n' number, 'o' operator or bracket
	text string
}

func (w sqlWord) upper() string { return strings.ToUpper(w.text) }

// sqlReserved are words that must not appear in a condition: they start
// another statement or clause, write files, lock rows, or run a subquery.
var sqlReserved = set("SELECT", "FROM", "INTO", "OUTFILE", "DUMPFILE", "UNION", "INTERSECT", "EXCEPT", "MINUS", "JOIN", "WHERE", "SET",
	"RETURNING", "LIMIT", "OFFSET", "FETCH", "ORDER", "GROUP", "HAVING", "FOR", "LOCK", "EXISTS", "ANY", "ALL", "SOME", "CASE", "WHEN",
	"THEN", "ELSE", "END", "CAST", "WITH", "VALUES", "DELETE", "UPDATE", "INSERT", "REPLACE", "DROP", "TRUNCATE", "ALTER", "CREATE",
	"GRANT", "REVOKE", "CALL", "DO", "COPY", "VACUUM", "USING", "ON", "WINDOW", "OVER", "PROCEDURE", "EXECUTE", "EXPLAIN", "ANALYZE",
	"USE", "SHOW", "TABLE", "DATABASE", "LATERAL")

// sqlLogic are the words a condition may use that are not names.
var sqlLogic = set("AND", "OR", "NOT", "IN", "IS", "NULL", "LIKE", "ILIKE", "BETWEEN", "TRUE", "FALSE")

// sqlOps are the operators and brackets a condition may use.
var sqlOps = set("=", "<>", "!=", "<=", ">=", "<", ">", "+", "-", "*", "/", "%", "(", ")", ",")

func isWordStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
func isDigit(c byte) bool     { return c >= '0' && c <= '9' }

// scan returns the end of the run of characters accepted by ok from i.
func scan(s string, i int, ok func(byte) bool) int {
	for i < len(s) && ok(s[i]) {
		i++
	}
	return i
}

func wordChar(c byte) bool { return isWordStart(c) || isDigit(c) }

// lexSQL splits one statement into words, strings, numbers and operators. It
// accepts only plain printable ASCII and refuses everything that could hide or
// chain something: a second statement, comments, backslashes, double quotes,
// backticks, dollar quoting, variables and casts.
func lexSQL(s string) ([]sqlWord, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimSpace(strings.TrimSuffix(s, ";"))
	if strings.Contains(s, "--") || strings.Contains(s, "/*") {
		return nil, false
	}
	var out []sqlWord
	for i := 0; i < len(s); {
		start, c := i, s[i]
		kind := byte('o')
		switch {
		case c == ' ':
			i++
			continue
		case c == '\'':
			kind = 's'
			for i++; i < len(s) && (s[i] != '\'' || i+1 < len(s) && s[i+1] == '\''); i++ {
				if s[i] < ' ' || s[i] > '~' || s[i] == '\\' {
					return nil, false
				}
				if s[i] == '\'' {
					i++ // the second quote of a doubled one
				}
			}
			if i >= len(s) {
				return nil, false
			}
			i++
		case isWordStart(c):
			kind = 'w'
			i = scan(s, i, wordChar)
			if i+1 < len(s) && s[i] == '.' && isWordStart(s[i+1]) {
				i = scan(s, i+1, wordChar)
			}
		case isDigit(c):
			kind = 'n'
			i = scan(s, i, isDigit)
			if i+1 < len(s) && s[i] == '.' && isDigit(s[i+1]) {
				i = scan(s, i+1, isDigit)
			}
			if i < len(s) && isWordStart(s[i]) {
				return nil, false
			}
		default:
			i++
			if i < len(s) && sqlOps[s[start:i+1]] {
				i++
			}
			if !sqlOps[s[start:i]] {
				return nil, false
			}
		}
		out = append(out, sqlWord{kind, s[start:i]})
	}
	return out, len(out) > 0
}

// plainName is a table name: a word that is not a keyword.
func plainName(w sqlWord) bool {
	return w.kind == 'w' && !sqlReserved[w.upper()] && !sqlLogic[w.upper()]
}

// parseChange reads DELETE FROM t [WHERE c], UPDATE t SET ... [WHERE c],
// DROP TABLE [IF EXISTS] t and TRUNCATE [TABLE] t, and nothing else.
func parseChange(sql string) (c sqlChange, ok bool) {
	w, ok := lexSQL(sql)
	if !ok {
		return c, false
	}
	up := func(i int) string {
		if i < len(w) {
			return w[i].upper()
		}
		return ""
	}
	named := func(i int) bool { return i < len(w) && plainName(w[i]) }
	// lone is a statement that is only its keywords and one table name at i.
	lone := func(verb string, i int) (sqlChange, bool) {
		if !named(i) || len(w) != i+1 {
			return c, false
		}
		return sqlChange{verb: verb, table: w[i].text}, true
	}
	switch {
	case up(0) == "DELETE" && up(1) == "FROM" && named(2):
		return sqlChange{verb: "delete", table: w[2].text}.withWhere(w[3:])
	case up(0) == "UPDATE" && named(1) && up(2) == "SET":
		c = sqlChange{verb: "update", table: w[1].text}
		for i := 3; i < len(w); i++ {
			switch u := up(i); u {
			case "WHERE":
				return c.withWhere(w[i:])
			case "FROM", "JOIN", "LIMIT", "ORDER", "RETURNING", "USING", "SELECT":
				return c, false
			}
		}
		return c, len(w) > 3 // no condition: every row
	case up(0) == "DROP" && up(1) == "TABLE":
		if up(2) == "IF" && up(3) == "EXISTS" {
			return lone("drop", 4)
		}
		return lone("drop", 2)
	case up(0) == "TRUNCATE":
		if up(1) == "TABLE" {
			return lone("truncate", 2)
		}
		return lone("truncate", 1)
	}
	return c, false
}

// withWhere reads what follows the table: nothing, or WHERE and a condition
// made only of names, literals, comparisons, brackets and AND/OR/NOT/IN/LIKE/
// BETWEEN/IS. A name followed by a bracket would be a function call, which
// could do anything, so it is refused.
func (c sqlChange) withWhere(w []sqlWord) (sqlChange, bool) {
	if len(w) == 0 {
		return c, true
	}
	if w[0].upper() != "WHERE" || len(w) == 1 {
		return c, false
	}
	cond := w[1:]
	depth := 0
	parts := make([]string, len(cond))
	for i, t := range cond {
		switch {
		case t.text == "(" && t.kind == 'o':
			depth++
		case t.text == ")" && t.kind == 'o':
			if depth--; depth < 0 {
				return c, false
			}
		case t.kind == 'w' && (sqlReserved[t.upper()] || strings.Contains(t.text, ".")):
			// a dotted word could be a function written as t.f (attribute notation)
			return c, false
		case t.kind == 'w' && !sqlLogic[t.upper()] && i+1 < len(cond) && cond[i+1].text == "(":
			return c, false // a function call
		}
		parts[i] = t.text
	}
	c.where = strings.Join(parts, " ")
	return c, depth == 0
}

func rows(n int) string {
	if n == 1 {
		return "1 row"
	}
	return report.Commas(n) + " rows"
}

// effect describes n rows touched, raising the level for a large count.
func (c sqlChange) effect(n int, query string) domain.Effect {
	e := domain.Effect{Kind: domain.EffectDatabase, Evidence: domain.Inspected, MinLevel: domain.R0}
	t := report.Printable(c.table)
	switch {
	case n == 0 && (c.verb == "drop" || c.verb == "truncate"):
		e.Summary = t + " is empty, but " + c.verb + " still removes its structure or resets it"
	case n == 0:
		e.Summary = "no rows match, so " + c.verb + " would change nothing in " + t
	case c.verb == "delete":
		e.Summary = "would delete " + rows(n) + " from " + t
	case c.verb == "update":
		e.Summary = "would change " + rows(n) + " in " + t
	default:
		e.Summary = c.verb + " would destroy " + t + ", which holds " + rows(n)
	}
	if n > 0 {
		e.MinLevel = domain.R2
	}
	if n >= dbLargeCount {
		e.MinLevel = domain.R3
	}
	e.Details = []string{"counted without changing anything, with: " + report.Printable(query)}
	return e
}
