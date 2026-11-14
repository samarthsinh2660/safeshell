package effects

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samarthsinh2660/safeshell/internal/domain"
	"github.com/samarthsinh2660/safeshell/internal/shellparse"
)

// neverRunsTheStatement fails when any query still holds a word of the
// statement being previewed: only the rebuilt count may be sent.
func neverRunsTheStatement(t *testing.T, f *fakeTool) {
	t.Helper()
	for _, c := range f.calls {
		for _, bad := range []string{"DELETE", "UPDATE", "DROP", "TRUNCATE", "INSERT", "FLUSH", " DEL ", "UNLINK"} {
			if strings.Contains(strings.ToUpper(c), bad) {
				t.Errorf("the statement itself was sent: %s", c)
			}
		}
	}
}

func couldNot(t *testing.T, es []domain.Effect) {
	t.Helper()
	e := one(t, es)
	if e.Kind != domain.EffectDatabase || e.Evidence != domain.Estimated || e.MinLevel != domain.R0 || !strings.Contains(e.Summary, "could not check") || !strings.Contains(e.Summary, "rule level stands") {
		t.Errorf("%+v", e)
	}
}

func TestPsqlCountsInAReadOnlyTransactionWithTheSameConnection(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"SELECT count": "12431\n"}}
	e := one(t, InspectDatabases(f.run, cmds(t, `psql -h db.example -p 5433 -U app -d shop -c "DELETE FROM orders WHERE status = 'old' AND (id < 100 OR total >= 1.5);"`)))
	if e.Kind != domain.EffectDatabase || e.Evidence != domain.Inspected || e.MinLevel != domain.R3 || e.Summary != "would delete 12,431 rows from orders" {
		t.Errorf("%+v", e)
	}
	want := "psql -X -w -q -A -t -v ON_ERROR_STOP=1 -h db.example -p 5433 -U app -d shop -c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM orders WHERE status = 'old' AND ( id < 100 OR total >= 1.5 ) -c ROLLBACK"
	if len(f.calls) != 1 || f.calls[0] != want {
		t.Errorf("got %q\nwant %q", f.calls, want)
	}
	if len(e.Details) != 1 || !strings.Contains(e.Details[0], "SELECT count(*) FROM orders WHERE") {
		t.Errorf("%v", e.Details)
	}
	neverRunsTheStatement(t, f)
}

func TestPsqlOptionSpellings(t *testing.T) {
	for line, want := range map[string]string{
		`psql -dshop -hdb -c "DELETE FROM t"`:                        "-d shop -h db -c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM t -c ROLLBACK",
		`psql --host=db --command="DELETE FROM t WHERE a = 1" shop`:  "--host db -c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM t WHERE a = 1 -c ROLLBACK shop",
		`psql --host db --port 1 -c "DELETE FROM s.t" shop app`:      "--host db --port 1 -c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM s.t -c ROLLBACK shop app",
		`psql -w -X --quiet -c "TRUNCATE TABLE t" "host=h dbname=d"`: "-w -X --quiet -c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM t -c ROLLBACK host=h dbname=d",
		`psql -c "UPDATE t SET a = 'it''s' , b = 2"`:                 "-c BEGIN READ ONLY -c SET LOCAL statement_timeout = '10s' -c SELECT count(*) FROM t -c ROLLBACK",
	} {
		f := &fakeTool{answers: map[string]string{"SELECT count": "3"}}
		e := one(t, InspectDatabases(f.run, cmds(t, line)))
		got := strings.TrimPrefix(f.calls[0], "psql -X -w -q -A -t -v ON_ERROR_STOP=1 ")
		if e.Evidence != domain.Inspected || got != want {
			t.Errorf("%s:\n got %q\nwant %q\n%+v", line, got, want, e)
		}
		neverRunsTheStatement(t, f)
	}
}

func TestMysqlCountsInAReadOnlyTransaction(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"SELECT count": "7\n"}}
	e := one(t, InspectDatabases(f.run, cmds(t, `mysql -h db -P 3307 -u root -D shop --execute "UPDATE users SET active = 0 WHERE last_login < '2020-01-01'"`)))
	if e.MinLevel != domain.R2 || e.Summary != "would change 7 rows in users" {
		t.Errorf("%+v", e)
	}
	want := "mysql -h db -P 3307 -u root -D shop --init-command= --batch --skip-column-names -e SET SESSION MAX_EXECUTION_TIME=10000; SET SESSION TRANSACTION READ ONLY; START TRANSACTION READ ONLY; SELECT count(*) FROM users WHERE last_login < '2020-01-01'; ROLLBACK"
	if f.calls[0] != want {
		t.Errorf("got %q\nwant %q", f.calls[0], want)
	}
	neverRunsTheStatement(t, f)
	f = &fakeTool{answers: map[string]string{"SELECT count": "1"}}
	one(t, InspectDatabases(f.run, cmds(t, `mysql --no-defaults --user=u --socket=/s -e "DELETE FROM t" shop`)))
	if !strings.HasPrefix(f.calls[0], "mysql --no-defaults --user u --socket /s --init-command= --batch") || !strings.HasSuffix(f.calls[0], "ROLLBACK shop") {
		t.Errorf("%q", f.calls)
	}
}

func TestSqliteOpensTheFileReadOnlyWithoutItsStartupFile(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"SELECT count": "3\n"}}
	e := one(t, InspectDatabases(f.run, cmds(t, `sqlite3 app.db "DELETE FROM logs WHERE level = 'debug'"`)))
	if e.MinLevel != domain.R2 || e.Summary != "would delete 3 rows from logs" {
		t.Errorf("%+v", e)
	}
	if want := "sqlite3 -init /dev/null -safe -readonly app.db SELECT count(*) FROM logs WHERE level = 'debug'"; f.calls[0] != want {
		t.Errorf("got %q\nwant %q", f.calls[0], want)
	}
	neverRunsTheStatement(t, f)
	// A relative file after a cd on the same line is not the one in the folder.
	c := cmds(t, `sqlite3 app.db "DELETE FROM logs"`)
	c[0].Cwd = "/elsewhere"
	couldNot(t, InspectDatabases(f.run, c))
	c[0].Args[0] = "/var/app.db"
	if es := InspectDatabases(f.run, c); es[0].Evidence != domain.Inspected {
		t.Errorf("%+v", es)
	}
}

func TestDropAndTruncateCountTheRowsTheyWouldDestroy(t *testing.T) {
	for line, want := range map[string]string{
		`psql -c "DROP TABLE orders"`:           "drop would destroy orders, which holds 1 row",
		`psql -c "DROP TABLE IF EXISTS orders"`: "drop would destroy orders, which holds 1 row",
		`psql -c "TRUNCATE orders"`:             "truncate would destroy orders, which holds 1 row",
		`psql -c "truncate table orders;"`:      "truncate would destroy orders, which holds 1 row",
	} {
		f := &fakeTool{answers: map[string]string{"SELECT count(*) FROM orders -c": "1"}}
		e := one(t, InspectDatabases(f.run, cmds(t, line)))
		if e.Summary != want || e.MinLevel != domain.R2 {
			t.Errorf("%s: %+v", line, e)
		}
		neverRunsTheStatement(t, f)
	}
}

func TestCountSizeDecidesTheLevel(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"SELECT count": "0"}}
	e := one(t, InspectDatabases(f.run, cmds(t, `psql -c "DROP TABLE t"`)))
	if e.MinLevel != domain.R0 || e.Summary != "t is empty, but drop still removes its structure or resets it" {
		t.Errorf("%+v", e)
	}
	for count, want := range map[string]domain.Level{"0": domain.R0, "1": domain.R2, "999": domain.R2, "1000": domain.R3} {
		f := &fakeTool{answers: map[string]string{"SELECT count": count}}
		e := one(t, InspectDatabases(f.run, cmds(t, `psql -c "UPDATE t SET a = 1"`)))
		if e.MinLevel != want || e.Evidence != domain.Inspected {
			t.Errorf("%s: %+v", count, e)
		}
		if count == "0" && e.Summary != "no rows match, so update would change nothing in t" {
			t.Errorf("%+v", e)
		}
	}
}

func TestStatementsThatAreNotSimpleAreNeverRunAndKeepTheRuleLevel(t *testing.T) {
	for _, stmt := range []string{
		"DELETE FROM t; DROP TABLE x",
		"DELETE FROM t WHERE a = 1 -- x",
		"DELETE FROM t WHERE a = 1 /* x */",
		"DELETE FROM t WHERE a IN (SELECT id FROM u)",
		"DELETE FROM t WHERE lower(a) = 'x'",
		"DELETE FROM t WHERE a = 1 RETURNING *",
		"DELETE FROM t USING u WHERE t.id = u.id",
		"DELETE FROM t WHERE a = 1 INTO OUTFILE '/tmp/x'",
		"DELETE FROM t WHERE a = 1 LIMIT 1",
		"DELETE FROM t WHERE",
		"DELETE FROM t WHERE a = (1",
		"DELETE FROM t WHERE a = 1)",
		"DELETE FROM t WHERE a = 1.5x",
		"DELETE FROM t WHERE a = 1e5",
		"DELETE FROM t WHERE a = 'open",
		"DELETE FROM t WHERE a = 'x''",
		"DELETE FROM t WHERE a = 'back\\\\slash'",
		"DELETE FROM t WHERE a = 'caf\u00e9'",
		"DELETE FROM t WHERE a = 'tab\there'",
		"DELETE FROM t WHERE a @ 1",
		"DELETE FROM t WHERE a ! 1",
		"DELETE FROM t WHERE a = $1",
		"DELETE FROM t WHERE a = `b`",
		"DELETE FROM t WHERE a.",
		"DELETE FROM t WHERE t.f = 1",
		"DELETE FROM t WHERE a = s.f",
		`DELETE FROM "t"`,
		"DELETE FROM select",
		"DELETE FROM a.b.c",
		"DELETE FROM t u",
		"DELETE FROM",
		"WITH x AS (SELECT 1) DELETE FROM t",
		"UPDATE t SET a = 1 FROM u WHERE t.id = u.id",
		"UPDATE t SET a = (SELECT 1) WHERE b = 1",
		"UPDATE t SET a = 1 ORDER BY id LIMIT 1",
		"UPDATE t SET a = 1 WHERE b = 1 RETURNING a",
		"UPDATE t SET",
		"DROP TABLE a, b",
		"DROP TABLE t CASCADE",
		"DROP TABLE IF EXISTS",
		"TRUNCATE t RESTART IDENTITY",
		"TRUNCATE",
		"TRUNCATE TABLE",
	} {
		f := &fakeTool{answers: map[string]string{"": "5"}}
		line := `psql -c '` + stmt + `'`
		if strings.Contains(stmt, "'") {
			line = `psql -c "` + strings.ReplaceAll(stmt, "`", "\\`") + `"`
		}
		res, err := shellparse.Parse(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		couldNot(t, InspectDatabases(f.run, res.Pipelines[0].Cmds))
		if len(f.calls) != 0 {
			t.Errorf("%s: ran %v", stmt, f.calls)
		}
	}
}

func TestOptionsThatCarryAPasswordOrReadOtherSettingsGiveUp(t *testing.T) {
	for _, line := range []string{
		`psql -c "DELETE FROM t WHERE id = 1" -- '-c\! touch /tmp/pwned'`,
		`psql -c "DELETE FROM t WHERE id = 1" -- -o/home/u/.bashrc`,
		`psql -c "DELETE FROM t WHERE id = 1" -- shop`,
		`mysql -e "DELETE FROM t WHERE id = 1" -- "--init-command=DROP TABLE prod"`,
		`mysql -e "DELETE FROM t WHERE id = 1" -- --tee=/tmp/x`,
		`sqlite3 a.db "DELETE FROM t" -- -init "x"`,
		`redis-cli -u redis://secret@h flushall`,
		`psql -c "DELETE FROM t" postgresql://app@h/db`,
		`psql -W -c "DELETE FROM t"`,
		`psql -c "DELETE FROM t" postgresql://u:pw@h/db`,
		`psql -c "DELETE FROM t" -d "host=h password=x"`,
		`psql -c "DELETE FROM t" -U "$USER"`,
		`psql -c "DELETE FROM t" 'db*'`,
		`psql -f x.sql -c "DELETE FROM t"`,
		`psql -o out -c "DELETE FROM t"`,
		`psql -c "DELETE FROM t" -c "DELETE FROM u"`,
		`psql -c "DELETE FROM t" --command "DELETE FROM u"`,
		`psql -c "DELETE FROM t" --bogus`,
		`psql -c "DELETE FROM t" -h`,
		`mysql -e "DELETE FROM t" -h`,
		`redis-cli DEL k -n`,
		`redis-cli -n DEL`,
		`redis-cli GET DEL`,
		`psql --host DELETE FROM t`,
		`psql -c "DELETE FROM t" a b c`,
		`psql -c "DELETE FROM t" -- a b c`,
		`psql -c "DELETE FROM t" -`,
		`mysql -psecret -e "DELETE FROM t"`,
		`mysql -p -e "DELETE FROM t"`,
		`mysql --password=x -e "DELETE FROM t"`,
		`mysql --defaults-file=x -e "DELETE FROM t"`,
		`mysql --login-path=x -e "DELETE FROM t"`,
		`mysql -e "DELETE FROM t" a b`,
		`mysql -h h "DELETE FROM t"`,
		`sqlite3 -cmd ".shell id" a.db "DELETE FROM t"`,
		`sqlite3 "DELETE FROM t"`,
		`sqlite3 a.db b.db "DELETE FROM t"`,
		`sqlite3 a.db "DELETE FROM t" ".shell id"`,
		`redis-cli -a secret FLUSHALL`,
		`redis-cli -u redis://:pw@h FLUSHALL`,
		`redis-cli -h $HOST FLUSHALL`,
		`redis-cli DEL`,
		`redis-cli DEL 'k*'`,
		`redis-cli FLUSHDB now`,
		`redis-cli -x DEL k`,
		`redis-cli --bogus DEL k`,
		`redis-cli --user DEL k`,
	} {
		f := &fakeTool{answers: map[string]string{"": "5"}}
		couldNot(t, InspectDatabases(f.run, cmds(t, line)))
		if len(f.calls) != 0 {
			t.Errorf("%s: ran %v", line, f.calls)
		}
	}
}

func TestOtherCommandsAndPlacesAreLeftAlone(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"": "5"}}
	for _, line := range []string{`psql -c "SELECT 1"`, `psql`, `mysql -e "SHOW TABLES"`, `sqlite3 a.db "select 1"`, `redis-cli GET k`, `redis-cli --scan`, `ls`, `echo DELETE FROM t`} {
		if es := InspectDatabases(f.run, cmds(t, line)); es != nil {
			t.Errorf("%s: %+v", line, es)
		}
	}
	remote := []shellparse.Command{{Name: "psql", Args: []string{"-c", "DELETE FROM t"}, Remote: "host"}}
	if es := InspectDatabases(f.run, remote); es != nil || len(f.calls) != 0 {
		t.Errorf("%+v %v", es, f.calls)
	}
}

func TestSudoAndPipedStatementsAreNotCounted(t *testing.T) {
	f := &fakeTool{answers: map[string]string{"": "5"}}
	couldNot(t, InspectDatabases(f.run, cmds(t, `sudo -u postgres psql -c "DELETE FROM t"`)))
	couldNot(t, InspectDatabases(f.run, cmds(t, `echo "DELETE FROM t WHERE a = 1" | psql -d shop`)))
	if len(f.calls) != 0 {
		t.Errorf("ran %v", f.calls)
	}
}

func TestACountThatFailsOrIsNotANumberNeverBecomesZeroRows(t *testing.T) {
	line := `psql -c "DELETE FROM orders WHERE a = 1"`
	for name, run := range map[string]Runner{
		"unreachable": func(string, ...string) (string, error) { return "", errors.New("could not connect") },
		"error with output": func(string, ...string) (string, error) {
			return "3\n", errors.New("exit status 3")
		},
		"empty":       func(string, ...string) (string, error) { return "", nil },
		"text":        func(string, ...string) (string, error) { return "ERROR: relation does not exist\n", nil },
		"two lines":   func(string, ...string) (string, error) { return "3\n4\n", nil },
		"negative":    func(string, ...string) (string, error) { return "-1\n", nil },
		"too large":   func(string, ...string) (string, error) { return "99999999999999999999\n", nil },
		"no timeouts": func(string, ...string) (string, error) { return "1 2\n", nil },
	} {
		for _, l := range []string{line, `mysql -e "DELETE FROM t"`, `sqlite3 a.db "DELETE FROM t"`, `redis-cli DEL a`} {
			es := InspectDatabases(run, cmds(t, l))
			if len(es) != 1 || es[0].Evidence != domain.Estimated || es[0].MinLevel != domain.R0 {
				t.Errorf("%s %s: %+v", name, l, es)
			}
		}
	}
}

func TestRedisCountsKeysWithReadOnlyQueries(t *testing.T) {
	for _, tc := range []struct {
		line, answer, summary, query string
		level                        domain.Level
	}{
		{"redis-cli -h cache -n 2 DEL a b", "(integer) 1\n", "would delete 1 key of the 2 key(s) you named", "redis-cli -h cache -n 2 EXISTS a b", domain.R2},
		{"redis-cli unlink a", "0\n", "would delete 0 keys of the 1 key(s) you named", "redis-cli EXISTS a", domain.R0},
		{"redis-cli --tls --user bob FLUSHDB ASYNC", "(integer) 5000\n", "would delete 5,000 keys in this database", "redis-cli --tls --user bob DBSIZE", domain.R3},
		{"redis-cli flushall", "# Keyspace\ndb0:keys=600,expires=0,avg_ttl=0\ndb3:keys=500,expires=1,avg_ttl=0\n", "would delete 1,100 keys in every database", "redis-cli INFO keyspace", domain.R3},
		{"redis-cli -u redis://h:6379/1 flushall sync", "# Keyspace\n", "would delete 0 keys in every database", "redis-cli -u redis://h:6379/1 INFO keyspace", domain.R0},
	} {
		f := &fakeTool{answers: map[string]string{"redis-cli": tc.answer}}
		e := one(t, InspectDatabases(f.run, cmds(t, tc.line)))
		if e.Kind != domain.EffectDatabase || e.Evidence != domain.Inspected || e.MinLevel != tc.level || e.Summary != tc.summary || f.calls[0] != tc.query {
			t.Errorf("%s: %+v %v", tc.line, e, f.calls)
		}
		neverRunsTheStatement(t, f)
	}
	for _, bad := range []string{"", "db0:keys=1", "# Keyspace\ndb0:keys=99999999999999999999,expires=0"} {
		if _, ok := keyspaceKeys(bad); ok {
			t.Errorf("%q", bad)
		}
	}
}

// realSqlite runs the real sqlite3 program, as the production runner would.
func realSqlite(home string) Runner {
	return func(name string, args ...string) (string, error) {
		cmd := exec.Command(name, args...)
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.Output()
		return string(out), err
	}
}

func TestSqlitePreviewLeavesTheDatabaseAndTheMachineUntouched(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	dir := t.TempDir()
	db := filepath.Join(dir, "app.db")
	if out, err := exec.Command("sqlite3", db, "create table logs(level text, n int); insert into logs values ('debug',1),('debug',2),('info',3);").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	before, _ := os.ReadFile(db)
	// A startup file that would run a program if it were read.
	home := t.TempDir()
	marker := filepath.Join(home, "ran")
	if err := os.WriteFile(filepath.Join(home, ".sqliterc"), []byte(".shell touch "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for stmt, want := range map[string]string{
		"DELETE FROM logs WHERE level = 'debug'": "would delete 2 rows from logs",
		"UPDATE logs SET n = 0":                  "would change 3 rows in logs",
		"DROP TABLE logs":                        "drop would destroy logs, which holds 3 rows",
		"DELETE FROM missing":                    "",
	} {
		es := InspectDatabases(realSqlite(home), cmds(t, "sqlite3 "+db+" \""+stmt+"\""))
		switch {
		case want == "":
			couldNot(t, es)
		case len(es) != 1 || es[0].Summary != want || es[0].Evidence != domain.Inspected:
			t.Errorf("%s: %+v", stmt, es)
		}
	}
	after, _ := os.ReadFile(db)
	if string(before) != string(after) {
		t.Error("the database file was changed by the preview")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the startup file ran")
	}
	if out, _ := exec.Command("sqlite3", db, "select count(*) from logs").Output(); strings.TrimSpace(string(out)) != "3" {
		t.Errorf("rows changed: %s", out)
	}
	es := InspectDatabases(realSqlite(home), cmds(t, "sqlite3 "+filepath.Join(dir, "none.db")+` "DELETE FROM logs"`))
	couldNot(t, es)
	if _, err := os.Stat(filepath.Join(dir, "none.db")); err == nil {
		t.Error("the preview created a database file")
	}
}
