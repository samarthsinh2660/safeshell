package undo

import (
	"os"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) []AptTx {
	t.Helper()
	b, err := os.ReadFile("testdata/history.log")
	if err != nil {
		t.Fatal(err)
	}
	return ParseAptHistory(string(b), time.UTC)
}

func at(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseAptHistory(t *testing.T) {
	txs := fixture(t)
	if len(txs) != 5 {
		t.Fatalf("got %d transactions", len(txs))
	}
	nginx := txs[1]
	if nginx.Commandline != "apt install -y nginx" || len(nginx.Installed) != 3 || nginx.Automatic != 2 || nginx.Upgraded != 1 || nginx.Removed != 1 {
		t.Errorf("nginx: %+v", nginx)
	}
	if !nginx.Start.Equal(at("2025-03-02 10:15:32")) {
		t.Errorf("start %v", nginx.Start)
	}
}

func TestAptRemovePlan(t *testing.T) {
	txs := fixture(t)
	tests := []struct {
		name, line, created string
		want                string // "" means no plan
		explain             []string
	}{
		{"one package", "apt install curl", "2025-03-01 09:00:00", "sudo apt remove curl:amd64", []string{"Removes the 1 packages"}},
		{"sudo and dependencies, upgrades not reversed", "sudo apt install -y nginx", "2025-03-02 10:15:30",
			"sudo apt remove libnginx-mod-http-geoip2:amd64 nginx:amd64 nginx-common:amd64",
			[]string{"3 packages", "2 of them only as dependencies", "upgraded 1 and removed 1", "not reversed"}},
		{"long line, other arch kept", "sudo apt install build-essential", "2025-03-03 08:29:59",
			"sudo apt remove binutils-common:amd64 libbinutils:amd64 libctf-nobfd0:amd64 libctf0:amd64 libgprofng0:amd64 libjansson4:amd64 binutils-x86-64-linux-gnu:amd64 binutils:amd64 gcc-13-base:i386 libc-dev-bin:amd64 make:amd64 dpkg-dev:amd64 build-essential:amd64",
			[]string{"13 packages", "12 of them"}},
		{"installed nothing", "apt-get install nginx", "2025-03-02 11:00:00", "", nil},
		{"different command line", "apt install vim", "2025-03-02 10:15:30", "", nil},
		{"upgrade is not an install", "apt upgrade", "2025-03-03 09:00:00", "", nil},
		{"run before the guard approved it", "apt install curl", "2025-03-01 09:00:05", "", nil},
		{"run long after", "apt install curl", "2025-02-28 08:00:00", "", nil},
		{"same second as the guard", "apt install curl", "2025-03-01 09:00:01", "sudo apt remove curl:amd64", []string{"1 packages"}},
		{"compound line", "apt install curl && true", "2025-03-01 09:00:00", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := AptRemovePlan(txs, tt.line, at(tt.created))
			if ok != (tt.want != "") || p.Command != tt.want {
				t.Fatalf("got %q, %v", p.Command, ok)
			}
			for _, s := range tt.explain {
				if !strings.Contains(p.Explain, s) {
					t.Errorf("explain %q lacks %q", p.Explain, s)
				}
			}
		})
	}
}

func TestAptRemovePlanRefusesOddNames(t *testing.T) {
	txs := []AptTx{{Start: at("2025-03-01 09:00:00"), Commandline: "apt install x", Installed: []string{"x;rm"}}}
	if _, ok := AptRemovePlan(txs, "apt install x", at("2025-03-01 09:00:00")); ok {
		t.Error("a name that is not a package name must give no plan")
	}
}

func TestAptInstall(t *testing.T) {
	for line, want := range map[string]bool{
		"apt install curl": true, "sudo apt-get -y install curl": true, "sudo -E apt install curl": true,
		"apt remove curl": false, "apt": false, "apt -y": false, "apt install curl; rm x": false,
		"apt install $(cat x)": false, "echo apt install": false, "": false,
	} {
		if AptInstall(line) != want {
			t.Errorf("AptInstall(%q) = %v", line, !want)
		}
	}
}

func TestParseServiceCmd(t *testing.T) {
	good := map[string]ServiceCmd{
		"systemctl stop nginx":              {Verb: "stop", Units: []string{"nginx"}},
		"sudo systemctl disable --now a b":  {Verb: "disable", Now: true, Units: []string{"a", "b"}},
		"sudo systemctl mask ssh.service":   {Verb: "mask", Units: []string{"ssh.service"}},
		"systemctl stop getty@tty1.service": {Verb: "stop", Units: []string{"getty@tty1.service"}},
	}
	for line, want := range good {
		got, ok := ParseServiceCmd(line)
		if !ok || got.Verb != want.Verb || got.Now != want.Now || strings.Join(got.Units, ",") != strings.Join(want.Units, ",") {
			t.Errorf("%q: %+v %v", line, got, ok)
		}
	}
	for _, line := range []string{"systemctl status nginx", "systemctl stop", "systemctl --user stop x", "systemctl stop x; rm y",
		"systemctl stop $X", "ls", "systemctl stop --force x", "systemctl", ""} {
		if _, ok := ParseServiceCmd(line); ok {
			t.Errorf("%q should not parse", line)
		}
	}
}

func TestServiceRestore(t *testing.T) {
	tests := []struct {
		name, line string
		st         UnitState
		want       string
	}{
		{"stop active", "systemctl stop nginx", UnitState{"active", "enabled"}, "sudo systemctl start nginx"},
		{"stop already stopped", "systemctl stop nginx", UnitState{"inactive", "enabled"}, ""},
		{"disable enabled", "systemctl disable nginx", UnitState{"active", "enabled"}, "sudo systemctl enable nginx"},
		{"disable runtime-enabled", "systemctl disable nginx", UnitState{"active", "enabled-runtime"}, "sudo systemctl enable nginx"},
		{"disable already disabled", "systemctl disable nginx", UnitState{"active", "disabled"}, ""},
		{"disable static", "systemctl disable nginx", UnitState{"active", "static"}, ""},
		{"disable now", "systemctl disable --now nginx", UnitState{"active", "enabled"}, "sudo systemctl enable nginx && sudo systemctl start nginx"},
		{"mask", "systemctl mask nginx", UnitState{"inactive", "disabled"}, "sudo systemctl unmask nginx"},
		{"mask now", "systemctl mask --now nginx", UnitState{"active", "enabled"}, "sudo systemctl unmask nginx && sudo systemctl start nginx"},
		{"mask already masked", "systemctl mask nginx", UnitState{"inactive", "masked"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, ok := ParseServiceCmd(tt.line)
			if !ok {
				t.Fatal("no parse")
			}
			p, ok := c.Restore(c.Units[0], tt.st)
			if ok != (tt.want != "") || p.Command != tt.want {
				t.Fatalf("got %q, %v", p.Command, ok)
			}
			if ok && !strings.Contains(p.Explain, "nginx was "+tt.st.Active+" and "+tt.st.Enabled+" before") {
				t.Errorf("explain %q", p.Explain)
			}
		})
	}
}
